package core

import (
	"database/sql"
	"testing"
)

func TestValidateResultUnresolvedMigrationHasEmptyPath(t *testing.T) {
	ctx := newMigrationInfoContext()
	row := &SchemaHistoryRow{
		InstalledRank: 1,
		Version:       "2",
		VersionValid:  true,
		Description:   "two",
		Type:          TypeSQLMigration,
		Script:        "V2__two.sql",
		Checksum:      sql.NullInt64{Int64: 123, Valid: true},
		Success:       true,
	}
	result := validateResult([]*MigrationInfo{{Applied: row, Context: ctx}})
	if result.Valid || len(result.InvalidMigrations) != 1 {
		t.Fatalf("期望 1 个校验失败，实际 %#v", result)
	}
	if got := result.InvalidMigrations[0].Path; got != "" {
		t.Fatalf("未解析迁移的 filepath 应为空，实际 %q", got)
	}
}
