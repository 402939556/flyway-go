package core

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

type migrationLock struct {
	conn    *sql.Conn
	dialect string
	key     int64
	name    string
	tx      *sql.Tx // PostgreSQL transactional lock transaction
	txn     bool
}

const pgAdvisoryLockBase = int64(77431708279161)

// javaStringHash replicates Java's String.hashCode, used by Flyway's PG lock key.
func javaStringHash(s string) int32 {
	var h int32
	for _, r := range []rune(s) {
		h = 31*h + int32(uint16(r))
	}
	return h
}

// acquireMigrationLock gets a database-level migration mutex equivalent to Flyway's schema-history lock.
func (f *Flyway) acquireMigrationLock(ctx context.Context) (*migrationLock, error) {
	conn, err := f.openConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取迁移锁连接失败: %w", err)
	}
	if err := f.configureSessionSchema(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}

	material := ""
	txn := false
	if f.dialect.Name() == "postgresql" {
		if f.cfg.PGTransactionalLock != nil {
			txn = *f.cfg.PGTransactionalLock
		} else {
			txn = true
		}
		material, err = f.pgLockMaterial(ctx, conn)
		if err != nil {
			conn.Close()
			return nil, err
		}
	} else {
		material, err = f.lockMaterial(ctx, conn)
		if err != nil {
			conn.Close()
			return nil, err
		}
	}

	var key int64
	if f.dialect.Name() == "postgresql" {
		key = pgAdvisoryLockBase + int64(javaStringHash(material))
	} else {
		h := fnv.New64a()
		_, _ = h.Write([]byte(material))
		key = int64(h.Sum64())
	}
	lock := &migrationLock{conn: conn, dialect: f.dialect.Name(), key: key,
		name: "flywaygo:" + fmt.Sprintf("%016x", key), txn: txn}

	retries := 50
	if f.cfg.LockRetryCount != nil {
		retries = *f.cfg.LockRetryCount
	}
	delay := f.cfg.LockRetryDelay
	if delay <= 0 {
		delay = time.Second
	}

	for attempt := 0; attempt < retries; attempt++ {
		acquired, aerr := lock.tryAcquire(ctx)
		if aerr != nil {
			lock.cleanup()
			return nil, aerr
		}
		if acquired {
			return lock, nil
		}
		if attempt == retries-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			lock.cleanup()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	lock.cleanup()
	return nil, fmt.Errorf("无法在 %d 次重试内获取迁移锁 %s", retries, material)
}

// lockMaterial is derived from the immutable schema list resolved by New().
// Re-reading configuration or current_schema() here would make the lock key
// depend on pooled connection state and diverge from Flyway's configured schema.
func (f *Flyway) lockMaterial(ctx context.Context, conn *sql.Conn) (string, error) {
	_ = ctx
	_ = conn
	return f.dialect.Name() + "|" + strings.Join(f.schemas, ",") + "|" + f.cfg.Table, nil
}

// pgLockMaterial reproduces Table.toString(), the source of Flyway's PG advisory key.
func (f *Flyway) pgLockMaterial(ctx context.Context, conn *sql.Conn) (string, error) {
	_ = ctx
	_ = conn
	schema := f.schemas[0]
	return f.dialect.Quote(schema) + "." + f.dialect.Quote(tableBaseName(f.cfg.Table)), nil
}

func (l *migrationLock) tryAcquire(ctx context.Context) (bool, error) {
	if l.dialect == "postgresql" {
		if l.txn {
			tx, err := l.conn.BeginTx(ctx, nil)
			if err != nil {
				return false, fmt.Errorf("开启 PostgreSQL 迁移锁事务失败: %w", err)
			}
			var acquired bool
			if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock($1)", l.key).Scan(&acquired); err != nil {
				_ = tx.Rollback()
				return false, fmt.Errorf("获取 PostgreSQL advisory lock 失败: %w", err)
			}
			if !acquired {
				_ = tx.Rollback()
				return false, nil
			}
			l.tx = tx
			return true, nil
		}
		var acquired bool
		if err := l.conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&acquired); err != nil {
			return false, fmt.Errorf("获取 PostgreSQL advisory lock 失败: %w", err)
		}
		return acquired, nil
	}
	var acquired sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", l.name).Scan(&acquired); err != nil {
		return false, fmt.Errorf("获取 MySQL GET_LOCK 失败: %w", err)
	}
	return acquired.Valid && acquired.Int64 == 1, nil
}

func (l *migrationLock) cleanup() {
	if l.txn && l.tx != nil {
		_ = l.tx.Rollback()
		l.tx = nil
	}
	_ = l.conn.Close()
}

func (l *migrationLock) Release() {
	if l.dialect == "postgresql" {
		if l.txn {
			if l.tx != nil {
				_ = l.tx.Commit()
				l.tx = nil
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.key)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = l.conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", l.name)
	}
	_ = l.conn.Close()
}
