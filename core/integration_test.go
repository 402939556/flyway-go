package core_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/linsu/flywaygo/core"
)

// 集成测试通过环境变量控制，避免在无数据库环境下失败：
//
//	FLYWAYGO_PG_DSN=host=... port=... user=... password=... dbname=... sslmode=disable
//	FLYWAYGO_MYSQL_DSN=user:pass@tcp(host:port)/dbname?parseTime=true
//
// 每个测试会 clean 目标库，务必指向专用测试库。

type testLogger struct {
	t *testing.T
}

func (l testLogger) Info(format string, args ...interface{}) {
	l.t.Logf(format, args...)
}
func (l testLogger) Warn(format string, args ...interface{}) {
	l.t.Logf("WARN: "+format, args...)
}

func openDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("打开连接失败: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	return db
}

func writeMigration(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFlyway(t *testing.T, driver, dsn, dir string, mut func(*core.Config)) *core.Flyway {
	t.Helper()
	db := openDB(t, driver, dsn)
	t.Cleanup(func() { db.Close() })

	cfg := core.Config{
		Driver:            driver,
		Locations:         []string{dir},
		Database:          db,
		ValidateOnMigrate: core.Bool(true),
	}
	if mut != nil {
		mut(&cfg)
	}
	fw, err := core.New(context.Background(), cfg, testLogger{t})
	if err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	return fw
}

// 每个集成用例开始前彻底清库，保证可重复运行。
// PostgreSQL 重建 public schema，MySQL 重建整个测试库，从而同时清理
// routine、event、trigger、sequence 等普通 DROP TABLE/VIEW 覆盖不到的残留对象。
func resetDB(t *testing.T, driver, dsn string) {
	t.Helper()
	db := openDB(t, driver, dsn)
	defer db.Close()
	ctx := context.Background()

	if driver == "postgres" {
		if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE"); err != nil {
			t.Fatalf("删除 public schema 失败: %v", err)
		}
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA public"); err != nil {
			t.Fatalf("创建 public schema 失败: %v", err)
		}
		return
	}

	if driver != "mysql" {
		t.Fatalf("不支持的集成测试驱动: %s", driver)
	}

	var database, charset, collation string
	if err := db.QueryRowContext(ctx,
		"SELECT DATABASE(), @@character_set_database, @@collation_database").Scan(&database, &charset, &collation); err != nil {
		t.Fatalf("读取 MySQL 当前库信息失败: %v", err)
	}
	if database == "" {
		t.Fatal("MySQL DSN 必须包含专用测试数据库名")
	}
	quoted := "`" + strings.ReplaceAll(database, "`", "``") + "`"
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoted); err != nil {
		t.Fatalf("删除 MySQL 测试库 %s 失败: %v", database, err)
	}
	create := "CREATE DATABASE " + quoted
	if charset != "" {
		create += " CHARACTER SET " + charset
	}
	if collation != "" {
		create += " COLLATE " + collation
	}
	if _, err := db.ExecContext(ctx, create); err != nil {
		t.Fatalf("重建 MySQL 测试库 %s 失败: %v", database, err)
	}
	if _, err := db.ExecContext(ctx, "USE "+quoted); err != nil {
		t.Fatalf("切换到 MySQL 测试库 %s 失败: %v", database, err)
	}
}

// 各数据库的用例主体完全一致，避免逻辑分叉带来的测试盲区。
func runScenarios(t *testing.T, driver, dsn string) {
	ctx := context.Background()

	t.Run("完整迁移流程", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__create_users.sql", `
CREATE TABLE users (
  id BIGINT PRIMARY KEY,
  name VARCHAR(100) NOT NULL
);`)
		writeMigration(t, dir, "V2__add_email.sql", `
ALTER TABLE users ADD COLUMN email VARCHAR(200);`)
		writeMigration(t, dir, "R__user_view.sql", `
CREATE VIEW v_users AS SELECT id, name FROM users;`)

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatalf("migrate 失败: %v", err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()

		// 表结构应就绪
		var cnt int
		q := "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'users' AND table_schema = DATABASE()"
		if driver == "postgres" {
			q = "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'users' AND table_schema = 'public'"
		}
		if err := db.QueryRow(q).Scan(&cnt); err != nil {
			t.Fatal(err)
		}
		if cnt != 3 {
			t.Errorf("users 应有 3 列，实际 %d", cnt)
		}

		// 历史表应有 3 条记录（2 个版本化 + 1 个可重复）
		var history int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history")).Scan(&history); err != nil {
			t.Fatal(err)
		}
		if history != 3 {
			t.Errorf("历史表应有 3 条，实际 %d", history)
		}

		// 再次 migrate 应幂等，不新增记录
		if err := fw.Migrate(ctx); err != nil {
			t.Fatalf("二次 migrate 失败: %v", err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history")).Scan(&history); err != nil {
			t.Fatal(err)
		}
		if history != 3 {
			t.Errorf("幂等性被破坏，历史表变为 %d 条", history)
		}
	})

	t.Run("校验和篡改应被拦截", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__create.sql", "CREATE TABLE t1 (id INT);")

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}

		// 修改已执行脚本内容 -> checksum 变化
		writeMigration(t, dir, "V1__create.sql", "CREATE TABLE t1 (id BIGINT);")
		fw2 := newFlyway(t, driver, dsn, dir, nil)
		err := fw2.Validate(ctx)
		if err == nil {
			t.Fatal("篡改后 validate 应失败，但通过了")
		}
		t.Logf("按预期拦截: %v", err)
	})

	t.Run("失败的迁移应被记录并可修复", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__ok.sql", "CREATE TABLE ok1 (id INT);")
		writeMigration(t, dir, "V2__bad.sql", "CREATE TABLE bad1 (id INT);\nTHIS IS NOT VALID SQL;")

		fw := newFlyway(t, driver, dsn, dir, nil)
		err := fw.Migrate(ctx)
		if err == nil {
			t.Fatal("非法 SQL 应导致 migrate 失败")
		}
		t.Logf("按预期失败: %v", err)

		db := openDB(t, driver, dsn)
		defer db.Close()
		var failed int
		qf := "SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE success = false"
		if driver == "mysql" {
			qf = "SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE success = 0"
		}
		if err := db.QueryRow(qf).Scan(&failed); err != nil {
			t.Fatal(err)
		}
		if driver == "mysql" {
			if failed != 1 {
				t.Errorf("MySQL 应有 1 条失败记录，实际 %d", failed)
			}
			// validate 应报出失败迁移
			fw2 := newFlyway(t, driver, dsn, dir, nil)
			if verr := fw2.Validate(ctx); verr == nil {
				t.Error("存在失败记录时 validate 应报错")
			}
			// repair 后失败记录应被清除
			if err := fw2.Repair(ctx); err != nil {
				t.Fatalf("repair 失败: %v", err)
			}
			if err := db.QueryRow(qf).Scan(&failed); err != nil {
				t.Fatal(err)
			}
			if failed != 0 {
				t.Errorf("repair 后不应有失败记录，实际 %d", failed)
			}
		} else if failed != 0 {
			t.Errorf("PostgreSQL 支持事务 DDL，失败迁移应回滚，实际 %d 条失败记录", failed)
		}
	})

	t.Run("版本顺序与数值排序", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		// 故意打乱创建顺序，验证按数值排序而非字典序
		writeMigration(t, dir, "V1.10__ten.sql", "CREATE TABLE t_1_10 (id INT);")
		writeMigration(t, dir, "V1.9__nine.sql", "CREATE TABLE t_1_9 (id INT);")
		writeMigration(t, dir, "V1__one.sql", "CREATE TABLE t_1 (id INT);")

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatalf("migrate 失败: %v", err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()
		rows, err := db.Query("SELECT version FROM " + quoteTable(driver, "flyway_schema_history") + " ORDER BY installed_rank")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var order []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			order = append(order, v)
		}
		want := []string{"1", "1.9", "1.10"}
		if len(order) != 3 {
			t.Fatalf("应有 3 条记录，实际 %d: %v", len(order), order)
		}
		for i := range want {
			if order[i] != want[i] {
				t.Errorf("执行顺序错误，期望 %v，实际 %v", want, order)
				break
			}
		}
	})

	t.Run("可重复迁移内容变化后重跑", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__t.sql", "CREATE TABLE src (id INT);")
		writeMigration(t, dir, "R__view.sql", "CREATE VIEW v_src AS SELECT id FROM src;")

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()

		// 内容变化 -> 应重跑
		var c1 int
		q := "SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE version IS NULL"
		if err := db.QueryRow(q).Scan(&c1); err != nil {
			t.Fatal(err)
		}

		writeMigration(t, dir, "R__view.sql", "CREATE OR REPLACE VIEW v_src AS SELECT id, id AS id2 FROM src;")
		fw2 := newFlyway(t, driver, dsn, dir, nil)
		if err := fw2.Migrate(ctx); err != nil {
			t.Fatalf("可重复迁移重跑失败: %v", err)
		}

		var c2 int
		if err := db.QueryRow(q).Scan(&c2); err != nil {
			t.Fatal(err)
		}
		if c2 <= c1 {
			t.Errorf("可重复迁移内容变化后应新增记录，之前 %d 条，之后 %d 条", c1, c2)
		}
	})

	t.Run("baseline 接管存量库", func(t *testing.T) {
		resetDB(t, driver, dsn)
		db := openDB(t, driver, dsn)
		defer db.Close()
		// 模拟已有业务表
		if _, err := db.ExecContext(ctx, "CREATE TABLE legacy (id INT)"); err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		writeMigration(t, dir, "V1__old.sql", "CREATE TABLE should_not_run_1 (id INT);")
		writeMigration(t, dir, "V2__new.sql", "CREATE TABLE new_table (id INT);")

		fw := newFlyway(t, driver, dsn, dir, func(c *core.Config) {
			c.BaselineVersion = "1"
			c.BaselineOnMigrate = true
		})
		if err := fw.Migrate(ctx); err != nil {
			t.Fatalf("带 baseline 的 migrate 失败: %v", err)
		}

		// V1 在基线之下，不应执行
		var n int
		q := "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'should_not_run_1' " + schemaCond(driver) + ""
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Error("低于基线的 V1 不应被执行")
		}
		// V2 在基线之上，应执行
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'new_table' " + schemaCond(driver) + "").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Error("高于基线的 V2 应被执行")
		}
	})

	t.Run("磁盘 baseline 脚本完整生命周期", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "B1__baseline.sql", "CREATE TABLE baseline_created (id INT);")
		writeMigration(t, dir, "V2__after_baseline.sql", "CREATE TABLE after_baseline (id INT);")

		fw := newFlyway(t, driver, dsn, dir, nil)
		entries, err := fw.Info(ctx)
		if err != nil {
			t.Fatalf("migrate 前 info 失败: %v", err)
		}
		b1 := findInfoByVersion(t, entries, "1")
		if b1.State != string(core.StatePending) || b1.Type != core.TypeSQLBaseline || b1.Category != "Baseline" {
			t.Fatalf("B1 初始状态错误: %#v", b1)
		}

		if err := fw.Migrate(ctx); err != nil {
			t.Fatalf("带磁盘 baseline 的 migrate 失败: %v", err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()
		var baselineRows, v2Rows int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE type = " + quoteLiteral(driver, core.TypeSQLBaseline)).Scan(&baselineRows); err != nil {
			t.Fatal(err)
		}
		if baselineRows != 1 {
			t.Fatalf("历史表应有 1 条 SQL_BASELINE 记录，实际 %d", baselineRows)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'baseline_created' " + schemaCond(driver)).Scan(&v2Rows); err != nil {
			t.Fatal(err)
		}
		if v2Rows != 1 {
			t.Fatal("磁盘 baseline 脚本应被执行")
		}

		fw2 := newFlyway(t, driver, dsn, dir, nil)
		entries, err = fw2.Info(ctx)
		if err != nil {
			t.Fatalf("migrate 后 info 失败: %v", err)
		}
		b1 = findInfoByVersion(t, entries, "1")
		if b1.State != string(core.StateBaseline) {
			t.Fatalf("应用后 B1 状态 = %q，期望 BASELINE", b1.State)
		}
		if v2 := findInfoByVersion(t, entries, "2"); v2.State != string(core.StateSuccess) {
			t.Fatalf("V2 状态 = %q，期望 SUCCESS", v2.State)
		}
		if err := fw2.Migrate(ctx); err != nil {
			t.Fatalf("二次 migrate 失败: %v", err)
		}
		var total int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history")).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != 2 {
			t.Fatalf("二次 migrate 不应重复执行，历史记录数 %d，期望 2", total)
		}
	})

	t.Run("磁盘 baseline 在已有版本后忽略", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__existing.sql", "CREATE TABLE existing_v1 (id INT);")
		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}

		writeMigration(t, dir, "B1__ignored_baseline.sql", "CREATE TABLE must_not_run (id INT);")
		fw2 := newFlyway(t, driver, dsn, dir, nil)
		entries, err := fw2.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b1 := findInfoByVersionAndType(t, entries, "1", core.TypeSQLBaseline)
		if b1.State != string(core.StateIgnored) || b1.Type != core.TypeSQLBaseline {
			t.Fatalf("已有 V1 时 B1 状态/类型错误: %#v", b1)
		}
		if err := fw2.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		db := openDB(t, driver, dsn)
		defer db.Close()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'must_not_run' " + schemaCond(driver)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("被忽略的同版本 baseline 不应执行")
		}
	})

	t.Run("同版本 V 与 B 脚本选择 baseline", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__versioned.sql", "CREATE TABLE versioned_v1 (id INT);")
		writeMigration(t, dir, "B1__baseline.sql", "CREATE TABLE baseline_b1 (id INT);")
		fw := newFlyway(t, driver, dsn, dir, nil)

		entries, err := fw.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b1 := findInfoByVersionAndType(t, entries, "1", core.TypeSQLBaseline)
		if b1.State != string(core.StatePending) {
			t.Fatalf("空库中同版本 B1 状态 = %q，期望 PENDING", b1.State)
		}
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()
		var baselineTable, versionedTable int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'baseline_b1' " + schemaCond(driver)).Scan(&baselineTable); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'versioned_v1' " + schemaCond(driver)).Scan(&versionedTable); err != nil {
			t.Fatal(err)
		}
		if baselineTable != 1 || versionedTable != 0 {
			t.Fatalf("同版本应只执行 B1: baseline=%d versioned=%d", baselineTable, versionedTable)
		}
	})

	t.Run("多个磁盘 baseline 只执行最高版本", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "B1__old_baseline.sql", "CREATE TABLE baseline_b1 (id INT);")
		writeMigration(t, dir, "B2__new_baseline.sql", "CREATE TABLE baseline_b2 (id INT);")
		fw := newFlyway(t, driver, dsn, dir, nil)

		entries, err := fw.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if b1 := findInfoByVersion(t, entries, "1"); b1.State != string(core.StateBelowBaseline) {
			t.Fatalf("B1 状态 = %q，期望 BELOW_BASELINE", b1.State)
		}
		if b2 := findInfoByVersion(t, entries, "2"); b2.State != string(core.StatePending) {
			t.Fatalf("B2 状态 = %q，期望 PENDING", b2.State)
		}
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		db := openDB(t, driver, dsn)
		defer db.Close()
		var b1Table, b2Table int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'baseline_b1' " + schemaCond(driver)).Scan(&b1Table); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'baseline_b2' " + schemaCond(driver)).Scan(&b2Table); err != nil {
			t.Fatal(err)
		}
		if b1Table != 0 || b2Table != 1 {
			t.Fatalf("应只执行最高 baseline B2: B1=%d B2=%d", b1Table, b2Table)
		}
	})

	t.Run("失败的磁盘 baseline 状态与 repair", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "B1__bad_baseline.sql", "THIS IS NOT VALID SQL;")
		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err == nil {
			t.Fatal("非法 baseline SQL 应导致 migrate 失败")
		}

		entries, err := fw.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b1 := findInfoByVersion(t, entries, "1")
		if driver == "mysql" {
			if b1.State != string(core.StateFailed) {
				t.Fatalf("MySQL 失败 baseline 状态 = %q，期望 FAILED", b1.State)
			}
			if err := fw.Repair(ctx); err != nil {
				t.Fatalf("repair 失败 baseline 失败: %v", err)
			}
			entries, err = fw.Info(ctx)
			if err != nil {
				t.Fatal(err)
			}
			b1 = findInfoByVersion(t, entries, "1")
			if b1.State != string(core.StatePending) {
				t.Fatalf("repair 后 baseline 状态 = %q，期望 PENDING", b1.State)
			}
		} else if b1.State != string(core.StatePending) {
			// PostgreSQL DDL transactions roll the failed baseline back entirely.
			t.Fatalf("PostgreSQL 失败 baseline 状态 = %q，期望 PENDING", b1.State)
		}
	})

	t.Run("缺失的磁盘 baseline 状态与 repair", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		baselinePath := filepath.Join(dir, "B1__missing_baseline.sql")
		writeMigration(t, dir, "B1__missing_baseline.sql", "CREATE TABLE missing_baseline_table (id INT);")
		writeMigration(t, dir, "V2__after.sql", "CREATE TABLE after_missing_baseline (id INT);")
		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(baselinePath); err != nil {
			t.Fatal(err)
		}

		fw2 := newFlyway(t, driver, dsn, dir, nil)
		entries, err := fw2.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b1 := findInfoByVersion(t, entries, "1")
		if b1.State != string(core.StateMissingSuccess) {
			t.Fatalf("缺失 B1 状态 = %q，期望 MISSING_SUCCESS", b1.State)
		}
		if err := fw2.Validate(ctx); err == nil {
			t.Fatal("缺失 baseline 时 validate 应失败")
		}
		if err := fw2.Repair(ctx); err != nil {
			t.Fatalf("repair 缺失 baseline 失败: %v", err)
		}
		if err := fw2.Validate(ctx); err != nil {
			t.Fatalf("repair 后 validate 应通过: %v", err)
		}
	})
	t.Run("clean 清空对象", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__a.sql", "CREATE TABLE a1 (id INT);")
		writeMigration(t, dir, "V2__b.sql", "CREATE TABLE b1 (id INT);")

		fw := newFlyway(t, driver, dsn, dir, func(c *core.Config) {
			c.CleanDisabled = core.Bool(false)
		})
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := fw.Clean(ctx); err != nil {
			t.Fatalf("clean 失败: %v", err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name IN ('a1','b1','flyway_schema_history') " + schemaCond(driver) + "").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("clean 后不应残留对象，实际 %d 个", n)
		}
	})

	t.Run("undo 回滚最新版本", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__create.sql", "CREATE TABLE u1 (id INT);")
		writeMigration(t, dir, "U1__drop.sql", "DROP TABLE u1;")

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := fw.Undo(ctx); err != nil {
			t.Fatalf("undo 失败: %v", err)
		}

		db := openDB(t, driver, dsn)
		defer db.Close()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'u1' " + schemaCond(driver) + "").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Error("undo 后表 u1 应被删除")
		}
		// 官方 undo 不物理删除 V1：保留 V1，新增 SQL_UNDO 与 DELETE 记录，info 显示 UNDONE。
		var v1, undoRows, deleteRows int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE version = '1' AND type = " + quoteLiteral(driver, "SQL")).Scan(&v1); err != nil {
			t.Fatal(err)
		}
		if v1 != 1 {
			t.Errorf("undo 后版本 1 的迁移记录应保留，实际 %d", v1)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE version = '1' AND type = " + quoteLiteral(driver, "SQL_UNDO")).Scan(&undoRows); err != nil {
			t.Fatal(err)
		}
		if undoRows != 1 {
			t.Errorf("undo 后应有 1 条 SQL_UNDO 记录，实际 %d", undoRows)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteTable(driver, "flyway_schema_history") + " WHERE version = '1' AND type = " + quoteLiteral(driver, "DELETE")).Scan(&deleteRows); err != nil {
			t.Fatal(err)
		}
		if deleteRows != 1 {
			t.Errorf("undo 后应有 1 条 DELETE 记录，实际 %d", deleteRows)
		}
		entries, err := fw.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range entries {
			if e.Version == "1" && e.State == string(core.StateUndone) {
				found = true
			}
		}
		if !found {
			t.Error("undo 后 info 应显示版本 1 为 UNDONE")
		}
	})

	t.Run("info 状态正确", func(t *testing.T) {
		resetDB(t, driver, dsn)
		dir := t.TempDir()
		writeMigration(t, dir, "V1__done.sql", "CREATE TABLE i1 (id INT);")

		fw := newFlyway(t, driver, dsn, dir, nil)
		if err := fw.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		// 新增一个未执行的脚本
		writeMigration(t, dir, "V2__pending.sql", "CREATE TABLE i2 (id INT);")
		fw2 := newFlyway(t, driver, dsn, dir, nil)

		entries, err := fw2.Info(ctx)
		if err != nil {
			t.Fatalf("info 失败: %v", err)
		}
		states := map[string]string{}
		for _, e := range entries {
			states[e.Version] = e.State
		}
		if states["1"] != string(core.StateSuccess) {
			t.Errorf("V1 状态应为 Success，实际 %q", states["1"])
		}
		if states["2"] != string(core.StatePending) {
			t.Errorf("V2 状态应为 Pending，实际 %q", states["2"])
		}
	})
}

func findInfoByVersion(t *testing.T, entries []core.InfoEntry, version string) core.InfoEntry {
	t.Helper()
	found := 0
	var result core.InfoEntry
	for _, entry := range entries {
		if entry.Version == version {
			found++
			result = entry
		}
	}
	if found != 1 {
		t.Fatalf("版本 %s 的 info 记录应有 1 条，实际 %d: %#v", version, found, entries)
	}
	return result
}

func findInfoByVersionAndType(t *testing.T, entries []core.InfoEntry, version, typ string) core.InfoEntry {
	t.Helper()
	found := 0
	var result core.InfoEntry
	for _, entry := range entries {
		if entry.Version == version && entry.Type == typ {
			found++
			result = entry
		}
	}
	if found != 1 {
		t.Fatalf("版本 %s 类型 %s 的 info 记录应有 1 条，实际 %d: %#v", version, typ, found, entries)
	}
	return result
}
func quoteTable(driver, name string) string {
	if driver == "mysql" {
		return "`" + name + "`"
	}
	return `"` + name + `"`
}

// schemaCond 返回限定当前数据库的过滤条件。
// MySQL 的 information_schema 会列出所有库，必须按 DATABASE() 过滤。
func schemaCond(driver string) string {
	if driver == "mysql" {
		return "AND table_schema = DATABASE()"
	}
	return "AND table_schema = 'public'"
}

func TestPostgres(t *testing.T) {
	dsn := os.Getenv("FLYWAYGO_PG_DSN")
	if dsn == "" {
		t.Skip("未设置 FLYWAYGO_PG_DSN，跳过 PostgreSQL 集成测试")
	}
	runScenarios(t, "postgres", dsn)
}

func TestMySQL(t *testing.T) {
	dsn := os.Getenv("FLYWAYGO_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 FLYWAYGO_MYSQL_DSN，跳过 MySQL 集成测试")
	}
	runScenarios(t, "mysql", dsn)
}

func quoteLiteral(driver, value string) string {
	if driver == "mysql" {
		return "'" + value + "'"
	}
	return "'" + value + "'"
}
