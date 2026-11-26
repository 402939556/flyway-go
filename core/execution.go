package core

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type statementCallbackEvents struct {
	Enabled bool
	Before  Event
	After   Event
	Error   Event
}

func migrationStatementEvents(m *Migration) statementCallbackEvents {
	if m != nil && m.Kind == KindUndo {
		return statementCallbackEvents{
			Enabled: true,
			Before:  EventBeforeEachUndoStatement,
			After:   EventAfterEachUndoStatement,
			Error:   EventAfterEachUndoStatementError,
		}
	}
	return statementCallbackEvents{
		Enabled: true,
		Before:  EventBeforeEachMigrateStatement,
		After:   EventAfterEachMigrateStatement,
		Error:   EventAfterEachMigrateStatementError,
	}
}

func executableStatements(m *Migration) []ParsedStatement {
	if m == nil {
		return nil
	}
	if len(m.Statements) != 0 {
		return m.Statements
	}
	out := make([]ParsedStatement, 0, len(m.SQL))
	for _, stmt := range m.SQL {
		out = append(out, ParsedStatement{SQL: stmt, CanExecuteInTransaction: true})
	}
	return out
}

// executeMigrationStatements executes parsed statements through the supplied
// executor. Keeping callbacks and statements on the same executor is required
// for transactional callback and group semantics.
func (f *Flyway) executeMigrationStatements(ctx context.Context, m *Migration, exec dbExecutor, events statementCallbackEvents) error {
	if exec == nil {
		exec = f.db
	}
	if m != nil && m.PlaceholderError != "" {
		return fmt.Errorf("解析 %s 的 placeholder 失败: %s", m.Script, m.PlaceholderError)
	}
	for i, stmt := range executableStatements(m) {
		if events.Enabled {
			if err := f.runCallbacksWithExecutor(ctx, events.Before, exec); err != nil {
				return err
			}
		}
		if err := f.executeParsedStatement(ctx, stmt, exec); err != nil {
			if events.Enabled {
				_ = f.runCallbacksWithExecutor(ctx, events.Error, exec)
			}
			return fmt.Errorf("迁移 %s 第 %d 条语句执行失败: %w\n语句: %s",
				m.Script, i+1, err, truncate(stmt.SQL, 500))
		}
		if events.Enabled {
			// Flyway executes afterEach*Statement callbacks only when the
			// statement succeeded. On error only afterEach*StatementError runs.
			if err := f.runCallbacksWithExecutor(ctx, events.After, exec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *Flyway) executeParsedStatement(ctx context.Context, stmt ParsedStatement, exec dbExecutor) error {
	if !stmt.Copy || f.dialect.Name() != "postgresql" {
		_, err := exec.ExecContext(ctx, stmt.SQL)
		return err
	}
	if tx, ok := exec.(*sql.Tx); ok {
		return executePostgresCopy(ctx, tx, stmt.SQL, stmt.CopyData)
	}
	// lib/pq only permits COPY FROM STDIN in a transaction. When a migration is
	// configured to run without an enclosing migration transaction, Flyway's
	// JDBC driver still sends COPY in an implicit transaction, so mirror that
	// with a short dedicated transaction.
	var tx *sql.Tx
	var err error
	if conn, ok := exec.(*sql.Conn); ok {
		tx, err = conn.BeginTx(ctx, nil)
	} else {
		tx, err = f.db.BeginTx(ctx, nil)
	}
	if err != nil {
		return err
	}
	if err := executePostgresCopy(ctx, tx, stmt.SQL, stmt.CopyData); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func executePostgresCopy(ctx context.Context, tx *sql.Tx, copySQL, data string) error {
	values, err := parsePostgresCopyText(data)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, copySQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range values {
		args := make([]any, len(row))
		for i := range row {
			args[i] = row[i]
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	// Exec with no values closes the COPY stream and waits for the server's
	// final result, including any deferred row-validation errors.
	_, err = stmt.ExecContext(ctx)
	return err
}

func parsePostgresCopyText(data string) ([][]any, error) {
	if data == "" {
		return nil, nil
	}
	lines := strings.Split(data, "\n")
	if len(lines) != 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	rows := make([][]any, 0, len(lines))
	for lineNo, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		fields := strings.Split(line, "\t")
		row := make([]any, len(fields))
		for i, field := range fields {
			value, err := decodePostgresCopyTextField(field)
			if err != nil {
				return nil, fmt.Errorf("COPY 数据第 %d 行第 %d 列解析失败: %w", lineNo+1, i+1, err)
			}
			row[i] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func decodePostgresCopyTextField(s string) (any, error) {
	if s == `\N` {
		return nil, nil
	}
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return nil, fmt.Errorf("字段以未完成的转义序列结尾")
		}
		i++
		switch s[i] {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '\\':
			b.WriteByte('\\')
		case 'x':
			start := i + 1
			end := start
			for end < len(s) && end < start+2 && isHexByte(s[end]) {
				end++
			}
			if end == start {
				b.WriteByte('x')
				continue
			}
			n, err := strconv.ParseUint(s[start:end], 16, 8)
			if err != nil {
				return nil, err
			}
			b.WriteByte(byte(n))
			i = end - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			start := i
			end := start + 1
			for end < len(s) && end < start+3 && s[end] >= '0' && s[end] <= '7' {
				end++
			}
			n, err := strconv.ParseUint(s[start:end], 8, 8)
			if err != nil {
				return nil, err
			}
			b.WriteByte(byte(n))
			i = end - 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// callbackScriptExecuteInTransaction mirrors ParserSqlScript.executeInTransaction():
// a script-level override wins; otherwise the script is transactional only if it
// contains no non-transactional statement. The global executeInTransaction flag
// separately controls whether an execution template actually wraps the script.
func callbackScriptExecuteInTransaction(cb callbackScript) bool {
	nonTx := false
	for _, stmt := range cb.Parsed {
		if !stmt.CanExecuteInTransaction {
			nonTx = true
			break
		}
	}
	return effectiveExecuteInTransaction(true, cb.ExecuteInTransaction, nonTx)
}

func callbackLogName(event Event, cb callbackScript) string {
	name := event.String()
	if cb.Description != "" {
		name += " - " + cb.Description
	}
	if !callbackScriptExecuteInTransaction(cb) {
		name += " [non-transactional]"
	}
	return name
}

func (f *Flyway) executeCallbackScript(ctx context.Context, event Event, cb callbackScript, exec dbExecutor) error {
	if cb.PlaceholderError != "" {
		return fmt.Errorf("Error while executing %s callback: 解析 %s 的 placeholder 失败: %s",
			event, cb.Script, cb.PlaceholderError)
	}
	for i, stmt := range cb.Parsed {
		if err := f.executeParsedStatement(ctx, stmt, exec); err != nil {
			return fmt.Errorf("Error while executing %s callback: callback %s 第 %d 条语句执行失败: %w\n语句: %s",
				event, cb.Script, i+1, err, truncate(stmt.SQL, 500))
		}
	}
	return nil
}

// runCallbacksWithExecutor executes callbacks on the supplied migration
// executor. It never starts its own transaction: each/statement callbacks must
// share the migration connection and its enclosing transaction.
func (f *Flyway) runCallbacksWithExecutor(ctx context.Context, event Event, exec dbExecutor) error {
	callbacks, err := f.loadCallbacks()
	if err != nil {
		return err
	}
	if exec == nil {
		exec = f.db
	}
	for _, cb := range callbacks {
		if cb.Event != event {
			continue
		}
		f.log("Executing SQL callback: %s", callbackLogName(event, cb))
		if err := f.executeCallbackScript(ctx, event, cb, exec); err != nil {
			return err
		}
	}
	return nil
}

// groupExecutionPlan mirrors DbMigrate.isExecuteGroupInTransaction. Flyway first
// checks that all migrations in the group agree on transaction capability. With
// mixed=false a mismatch is a configuration error; with mixed=true the group is
// executed migration-by-migration. A shared transaction is used only when every
// migration is transactional and the database supports transactional DDL.
func (f *Flyway) groupExecutionPlan(pending []*MigrationInfo) (bool, error) {
	if len(pending) == 0 || !f.cfg.Group || !f.dialect.SupportsDDLTransactions() {
		return false, nil
	}
	var groupTx *bool
	for _, info := range pending {
		if info == nil || info.Resolved == nil {
			return false, nil
		}
		m := info.Resolved
		canTx := effectiveExecuteInTransaction(*f.cfg.ExecuteInTransaction, m.ExecuteInTransaction, m.NonTransactional)
		if groupTx == nil {
			groupTx = Bool(canTx)
			continue
		}
		if *groupTx == canTx {
			continue
		}
		if !*f.cfg.Mixed {
			display := ""
			if m.Kind != KindRepeatable {
				display = m.Version.String()
			}
			if m.Description != "" {
				display += " " + m.Description
			}
			suffix := ""
			if !canTx {
				suffix = " [non-transactional]"
			}
			return false, fmt.Errorf(
				"Detected both transactional and non-transactional migrations within the same migration group (even though mixed is false). First offending migration: %q%s",
				display, suffix,
			)
		}
		return false, nil
	}
	return groupTx != nil && *groupTx, nil
}

func (f *Flyway) applyGroupInSharedTx(ctx context.Context, pending []*MigrationInfo, installedBy string, isPreviousVersioned *bool) error {
	conn, err := f.openConn(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移组连接失败: %w", err)
	}
	defer conn.Close()
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, info := range pending {
		m := info.Resolved
		if *isPreviousVersioned && m.Kind == KindRepeatable {
			if err := f.runCallbacks(ctx, EventAfterVersioned); err != nil {
				return err
			}
			if err := f.runCallbacks(ctx, EventBeforeRepeatables); err != nil {
				return err
			}
			*isPreviousVersioned = false
		}
		rank, err := f.history.nextRank(ctx, tx, TypeSQLMigration)
		if err != nil {
			return err
		}
		before, after := migrationLifecycleEvents(m)
		start := time.Now()
		if err := f.runCallbacksWithExecutor(ctx, before, tx); err != nil {
			return err
		}
		execErr := f.executeMigrationStatements(ctx, m, tx, migrationStatementEvents(m))
		if execErr != nil {
			if cbErr := f.runCallbacksWithExecutor(ctx, migrationLifecycleErrorEvent(m), tx); cbErr != nil {
				f.warn("执行迁移错误 callback 失败: %v", cbErr)
			}
			return execErr
		}
		if err := f.runCallbacksWithExecutor(ctx, after, tx); err != nil {
			return err
		}
		elapsed := int(time.Since(start).Milliseconds())
		if err := f.history.addApplied(ctx, tx, m, rank, elapsed, installedBy); err != nil {
			return err
		}
		f.log("  -> %s 成功，耗时 %d ms", m.String(), elapsed)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func migrationLifecycleEvents(m *Migration) (Event, Event) {
	if m != nil && m.Kind == KindUndo {
		return EventBeforeEachUndo, EventAfterEachUndo
	}
	return EventBeforeEachMigrate, EventAfterEachMigrate
}

func migrationLifecycleErrorEvent(m *Migration) Event {
	if m != nil && m.Kind == KindUndo {
		return EventAfterEachUndoError
	}
	return EventAfterEachMigrateError
}
