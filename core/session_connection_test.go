package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type scriptedConnState struct {
	mu           sync.Mutex
	openAttempts int
	failOpens    int
	failAlways   bool
	begins       int
	closes       int
	execs        []string
	execErr      error
}

func (s *scriptedConnState) nextOpen() (driver.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openAttempts++
	if s.failAlways || s.openAttempts <= s.failOpens {
		return nil, errors.New("scripted connection failure")
	}
	return &scriptedConn{state: s}, nil
}

type scriptedDriver struct {
	state *scriptedConnState
}

func (d *scriptedDriver) Open(string) (driver.Conn, error) {
	return d.state.nextOpen()
}

type scriptedConn struct {
	state *scriptedConnState
}

func (c *scriptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare not implemented")
}

func (c *scriptedConn) Close() error {
	c.state.mu.Lock()
	c.state.closes++
	c.state.mu.Unlock()
	return nil
}

func (c *scriptedConn) Begin() (driver.Tx, error) {
	c.state.mu.Lock()
	c.state.begins++
	c.state.mu.Unlock()
	return nil, errors.New("Begin not implemented")
}

func (c *scriptedConn) Ping(context.Context) error { return nil }

func (c *scriptedConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.execs = append(c.state.execs, query)
	if c.state.execErr != nil {
		return nil, c.state.execErr
	}
	return driver.RowsAffected(0), nil
}

func (c *scriptedConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	// openConn probes PostgreSQL's version. Returning a valid value keeps the
	// fake database path representative while schema resolution is not used.
	return &singleValueRows{columns: []string{"value"}, values: [][]driver.Value{{"150000"}}}, nil
}

type singleValueRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *singleValueRows) Columns() []string { return r.columns }
func (r *singleValueRows) Close() error      { return nil }
func (r *singleValueRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

var scriptedDriverID atomic.Uint64

func newScriptedFlyway(t *testing.T, state *scriptedConnState, cfg Config) (*Flyway, *sql.DB) {
	t.Helper()
	name := "flywaygo-scripted-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + strconvUint(scriptedDriverID.Add(1))
	sql.Register(name, &scriptedDriver{state: state})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg.Database = db
	cfg.setDefaults()
	dialect, err := GetDialect(cfg.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return &Flyway{cfg: cfg, db: db, dialect: dialect}, db
}

func strconvUint(v uint64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	return string(buf[i:])
}

func captureConnectDelays(t *testing.T) *[]time.Duration {
	t.Helper()
	original := connectRetrySleep
	delays := make([]time.Duration, 0, 4)
	connectRetrySleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	t.Cleanup(func() { connectRetrySleep = original })
	return &delays
}

func TestOpenConnRetriesWithExponentialBackoff(t *testing.T) {
	state := &scriptedConnState{failOpens: 2}
	fw, _ := newScriptedFlyway(t, state, Config{Driver: "mysql", ConnectRetries: 3})
	interval := 120
	fw.cfg.ConnectRetriesInterval = &interval
	delays := captureConnectDelays(t)

	conn, err := fw.openConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if state.openAttempts != 3 {
		t.Fatalf("open attempts = %d, want 3", state.openAttempts)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if !reflect.DeepEqual(*delays, want) {
		t.Fatalf("retry delays = %#v, want %#v", *delays, want)
	}
}

func TestOpenConnRetryExhaustion(t *testing.T) {
	state := &scriptedConnState{failAlways: true}
	fw, _ := newScriptedFlyway(t, state, Config{Driver: "mysql", ConnectRetries: 2})
	delays := captureConnectDelays(t)

	_, err := fw.openConn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "连接数据库失败") {
		t.Fatalf("error = %v, want connection failure", err)
	}
	if state.openAttempts != 3 {
		t.Fatalf("open attempts = %d, want initial attempt plus 2 retries", state.openAttempts)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if !reflect.DeepEqual(*delays, want) {
		t.Fatalf("retry delays = %#v, want %#v", *delays, want)
	}
}

func TestOpenConnRetryIntervalCap(t *testing.T) {
	state := &scriptedConnState{failAlways: true}
	fw, _ := newScriptedFlyway(t, state, Config{Driver: "mysql", ConnectRetries: 4})
	interval := 3
	fw.cfg.ConnectRetriesInterval = &interval
	delays := captureConnectDelays(t)

	if _, err := fw.openConn(context.Background()); err == nil {
		t.Fatal("expected connection failure")
	}
	want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}
	if !reflect.DeepEqual(*delays, want) {
		t.Fatalf("retry delays = %#v, want %#v", *delays, want)
	}
}

func TestOpenConnZeroRetryIntervalMatchesBackoffStrategy(t *testing.T) {
	state := &scriptedConnState{failAlways: true}
	fw, _ := newScriptedFlyway(t, state, Config{Driver: "mysql", ConnectRetries: 3})
	interval := 0
	fw.cfg.ConnectRetriesInterval = &interval
	delays := captureConnectDelays(t)

	if _, err := fw.openConn(context.Background()); err == nil {
		t.Fatal("expected connection failure")
	}
	want := []time.Duration{time.Second, 0, 0}
	if !reflect.DeepEqual(*delays, want) {
		t.Fatalf("retry delays = %#v, want %#v", *delays, want)
	}
}

func TestOpenConnExecutesInitSQLWithoutPlaceholdersOrTransactions(t *testing.T) {
	state := &scriptedConnState{}
	fw, _ := newScriptedFlyway(t, state, Config{
		Driver:  "mysql",
		InitSQL: "SELECT '${dbName}';\nCREATE TABLE init_marker(id int);",
	})

	conn, err := fw.openConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	want := []string{"SELECT '${dbName}'", "CREATE TABLE init_marker(id int)"}
	if !reflect.DeepEqual(state.execs, want) {
		t.Fatalf("initSql statements = %#v, want %#v", state.execs, want)
	}
	if state.begins != 0 {
		t.Fatalf("transactions started = %d, want 0", state.begins)
	}
}

func TestOpenConnExecutesInitSQLOnEachPhysicalConnection(t *testing.T) {
	state := &scriptedConnState{}
	fw, _ := newScriptedFlyway(t, state, Config{Driver: "mysql", InitSQL: "SELECT 1;"})

	first, err := fw.openConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := fw.openConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	_ = second.Close()

	want := []string{"SELECT 1", "SELECT 1"}
	if !reflect.DeepEqual(state.execs, want) {
		t.Fatalf("initSql statements = %#v, want %#v", state.execs, want)
	}
}

func TestOpenConnInitSQLFailureClosesConnection(t *testing.T) {
	state := &scriptedConnState{execErr: errors.New("init failed")}
	fw, db := newScriptedFlyway(t, state, Config{Driver: "mysql", InitSQL: "SELECT 1;"})

	_, err := fw.openConn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "执行 initSql 失败") {
		t.Fatalf("error = %v, want initSql failure", err)
	}
	if inUse := db.Stats().InUse; inUse != 0 {
		t.Fatalf("in-use connections = %d, want 0 after initSql failure", inUse)
	}
}
