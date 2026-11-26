package core

import (
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 版本号：与官方 MigrationVersion 行为逐条对齐 ----

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int // -1, 0, 1
	}{
		{"1", "1", 0},
		{"1", "1.0", 0},
		{"1.0.0", "1", 0},
		{"1.9", "1.10", -1},
		{"1.10", "1.9", 1},
		{"2", "1.999", 1},
		{"1.2.3", "1.2", 1},
		{"20230101", "20230102", -1},
		{"1_2", "1.2", 0}, // 下划线等价于点
		{"10", "9", 1},    // 数值比较而非字典序
		{"1.0.1", "1.1", -1},
	}
	for _, c := range cases {
		a, err := ParseVersion(c.a)
		if err != nil {
			t.Fatalf("parse %q: %v", c.a, err)
		}
		b, err := ParseVersion(c.b)
		if err != nil {
			t.Fatalf("parse %q: %v", c.b, err)
		}
		got := a.Compare(b)
		if got != c.want {
			t.Errorf("Compare(%s,%s) = %d, 期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// 官方规则：分裂正则 "\\.(?=\\d)"，因此 "1..2" 不会被切段，
// 而是整体交给 BigInteger 解析从而抛错。
func TestVersionInvalid(t *testing.T) {
	bad := []string{"abc", "1.", ".1", "1..2", "1.a", "1.-2", " 1"}
	for _, s := range append(bad, "") {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) 本应报错但通过", s)
		}
	}
}

func TestVersionBigNumber(t *testing.T) {
	// 超出 int64 的版本号应可解析并正确比较
	a, err := ParseVersion("99999999999999999999999999")
	if err != nil {
		t.Fatalf("大版本号解析失败: %v", err)
	}
	b := MustParseVersion("99999999999999999999999998")
	if a.Compare(b) != 1 {
		t.Error("大版本号比较错误")
	}
}

func TestVersionDisplay(t *testing.T) {
	v := MustParseVersion("1_2_3")
	if v.String() != "1.2.3" {
		t.Errorf("display 应为 1.2.3，实际 %s", v.String())
	}
}

func TestVersionRejectsHistoryDisplayText(t *testing.T) {
	displayValues := []string{
		"<< Latest Version >>",
		"<< Current Version >>",
		"<< Next Version >>",
		"<< Empty Schema >>",
	}
	for _, s := range displayValues {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) 本应报错但通过", s)
		}
	}
	for _, s := range []string{"latest", "LATEST", "current", "NEXT"} {
		if _, err := ParseVersion(s); err != nil {
			t.Errorf("ParseVersion(%q) 应识别为预定义版本: %v", s, err)
		}
	}
}

func TestVersionPredefined(t *testing.T) {
	empty := EmptyVersion()
	latest := MustParseVersion("latest")
	v1 := MustParseVersion("1")
	if empty.Compare(v1) != -1 {
		t.Error("EMPTY 应小于任何版本")
	}
	if latest.Compare(v1) != 1 {
		t.Error("LATEST 应大于任何版本")
	}
	if got := latest.GetVersion(); got != "9223372036854775807" {
		t.Errorf("LATEST.GetVersion() = %q", got)
	}
	if got := MustParseVersion("9223372036854775807"); got.Compare(latest) != 0 || !got.IsLatest() {
		t.Errorf("Long.MAX_VALUE 应解析为 LATEST，实际 %s", got)
	}
	if MustParseVersion("next").Compare(MustParseVersion("next")) != -1 {
		t.Error("官方 NEXT.compareTo(NEXT) 为非对称的 -1")
	}
}

// ---- checksum：用独立实现交叉验证 ----

// referenceChecksum 用另一种写法重算，避免与 Checksum 同源导致自证。
func referenceChecksum(content string) int32 {
	raw := []byte(content)
	// 手工按 \n / \r\n 切行
	var lines []string
	cur := []byte{}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			lines = append(lines, string(cur))
			cur = cur[:0]
		} else if raw[i] == '\r' {
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
			lines = append(lines, string(cur))
			cur = cur[:0]
		} else {
			cur = append(cur, raw[i])
		}
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}

	h := crc32.NewIEEE()
	for i, ln := range lines {
		if i == 0 {
			ln = strings.TrimPrefix(ln, "\uFEFF")
		}
		h.Write([]byte(ln))
	}
	return int32(h.Sum32())
}

func TestChecksumMatchesReference(t *testing.T) {
	samples := []string{
		"",
		"SELECT 1;",
		"SELECT 1;\n",
		"SELECT 1;\r\nSELECT 2;\r\n",
		"CREATE TABLE t (\n  id int\n);\n",
		"中文内容测试\n第二行\n",
		"\uFEFFSELECT 1;\nSELECT 2;",
	}
	for _, s := range samples {
		got := Checksum(s)
		want := referenceChecksum(s)
		if got != int64(want) {
			t.Errorf("checksum 不一致 for %q: got %d, want %d", s, got, want)
		}
	}
}

// 关键性质：Flyway 注释明确写着 "The checksum is encoding and line-ending independent"，
// 因为它是逐行喂入 CRC32 且不带换行符。
func TestChecksumLineEndingIndependent(t *testing.T) {
	lf := "CREATE TABLE a (id int);\nCREATE TABLE b (id int);\n"
	crlf := "CREATE TABLE a (id int);\r\nCREATE TABLE b (id int);\r\n"
	cr := "CREATE TABLE a (id int);\rCREATE TABLE b (id int);\r"
	if Checksum(lf) != Checksum(crlf) || Checksum(lf) != Checksum(cr) {
		t.Errorf("换行风格不应影响校验和: LF=%d CRLF=%d CR=%d",
			Checksum(lf), Checksum(crlf), Checksum(cr))
	}
}

func TestChecksumTrailingNewlineIrrelevant(t *testing.T) {
	a := "SELECT 1;"
	b := "SELECT 1;\n"
	if Checksum(a) != Checksum(b) {
		t.Error("末尾换行不应影响校验和")
	}
}

func TestChecksumDetectsChange(t *testing.T) {
	a := "CREATE TABLE a (id int);"
	b := "CREATE TABLE a (id bigint);"
	if Checksum(a) == Checksum(b) {
		t.Error("内容不同校验和不应相同")
	}
}

func TestChecksumOnlyFirstLineBOM(t *testing.T) {
	// 第二行的 BOM 字符应被计入（只有第一行剥离 BOM）
	a := "SELECT 1;\n\uFEFFSELECT 2;"
	b := "SELECT 1;\nSELECT 2;"
	if Checksum(a) == Checksum(b) {
		t.Error("非首行 BOM 不应被剥离")
	}
}

// ---- 语句拆分 ----

func TestSplitStatementsBasic(t *testing.T) {
	sql := "CREATE TABLE a (id int);\nINSERT INTO a VALUES (1);\nINSERT INTO a VALUES (2);"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("期望 3 条语句，实际 %d: %#v", len(got), got)
	}
	if strings.HasSuffix(got[0], ";") {
		t.Errorf("语句不应保留结尾分隔符: %q", got[0])
	}
}

func TestSplitStatementsSemicolonInString(t *testing.T) {
	sql := "INSERT INTO a VALUES ('x;y');\nINSERT INTO a VALUES ('z');"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("字符串内分号不应拆分，期望 2 条，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsComments(t *testing.T) {
	sql := "-- 注释;含分号\nCREATE TABLE a (id int);\n/* 块注释; 分号 */\nCREATE TABLE b (id int);"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsDollarQuoted(t *testing.T) {
	sql := "CREATE FUNCTION f() RETURNS int AS $$\nBEGIN\n  RETURN 1; -- 分号\nEND;\n$$ LANGUAGE plpgsql;\nSELECT f();"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsNamedDollarTag(t *testing.T) {
	sql := "CREATE FUNCTION f() RETURNS int AS $body$\nSELECT 1;\n$body$ LANGUAGE sql;\nSELECT 1;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("命名美元标记应被识别，期望 2 条，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsMySQLDelimiter(t *testing.T) {
	sql := "DELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND$$\nDELIMITER ;\nSELECT 3;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d: %#v", len(got), got)
	}
	if got[1] != "SELECT 3" {
		t.Errorf("第二条应为 SELECT 3，实际 %q", got[1])
	}
}

func TestSplitStatementsEscapedQuote(t *testing.T) {
	sql := "INSERT INTO a VALUES ('it''s; ok');\nSELECT 1;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsBackslashEscape(t *testing.T) {
	sql := "INSERT INTO a VALUES ('a\\';b');\nSELECT 1;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("反斜杠转义引号识别失败，期望 2 条，实际 %d: %#v", len(got), got)
	}
}

func TestSplitStatementsNestedComments(t *testing.T) {
	sql := "/* outer\n   /* inner */\n   INVALID;\n*/\nSELECT 1;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "SELECT 1" {
		t.Fatalf("嵌套注释应整体吞掉，实际 %#v", got)
	}
}

func TestSplitStatementsMySQLDelimiterDollar(t *testing.T) {
	sql := "CREATE TABLE a(id int);\nDELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\nEND$$\nDELIMITER ;\nINSERT INTO a VALUES (1);"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("期望 3 条语句，实际 %d: %#v", len(got), got)
	}
	if got[1] == "" || strings.Contains(got[1], "$$") || strings.Contains(got[1], "DELIMITER") {
		t.Fatalf("过程语句应去掉 $$ 且不包含 DELIMITER: %#v", got[1])
	}
}

func TestSplitStatementsOnlyComments(t *testing.T) {
	got, err := SplitStatements("-- 只有注释\n-- 没有语句")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("纯注释不应产生语句，实际 %#v", got)
	}
}

func TestSplitStatementsBacktick(t *testing.T) {
	sql := "CREATE TABLE `a;b` (id int);\nSELECT 1;"
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("反引号内分号不应拆分，期望 2 条，实际 %d: %#v", len(got), got)
	}
}

// ---- 文件名解析与扫描 ----

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanNamesAndOrder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"V1__init.sql":         "CREATE TABLE a (id int);",
		"V1.10__add_col.sql":   "ALTER TABLE a ADD COLUMN b int;",
		"V1.9__add_col2.sql":   "ALTER TABLE a ADD COLUMN c int;",
		"V2__create_index.sql": "CREATE INDEX i ON a (id);",
		"R__refresh_view.sql":  "CREATE OR REPLACE VIEW v AS SELECT 1;",
		"not_a_migration.txt":  "ignored",
		"readme.md":            "ignored",
	})

	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 {
		t.Fatalf("期望 5 个迁移，实际 %d", len(ms))
	}
	wantOrder := []string{"1", "1.9", "1.10", "2", ""}
	for i, w := range wantOrder {
		if ms[i].Version.String() != w {
			t.Errorf("第 %d 个版本应为 %q，实际 %q（脚本 %s）", i, w, ms[i].Version.String(), ms[i].Script)
		}
	}
	if !ms[4].Repeatable() {
		t.Error("最后一个应为可重复迁移")
	}
}

func TestScanDescriptionNormalization(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"V1__create-users_table.sql": "SELECT 1;",
		"R__user-view.sql":           "SELECT 2;",
	})
	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ms[0].Description != "create-users table" {
		t.Errorf("description 应为 'create-users table'，实际 %q", ms[0].Description)
	}
	if ms[1].Description != "user-view" {
		t.Errorf("repeatable description 应为 'user-view'，实际 %q", ms[1].Description)
	}
}

func TestScanDuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"V1__a.sql": "SELECT 1;",
		"V1__b.sql": "SELECT 2;",
	})
	s := &Scanner{Naming: DefaultNaming()}
	if _, err := s.Scan(dir); err == nil {
		t.Fatal("重复版本号应报错")
	}
}

func TestScanNestedDirs(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"sub/V1__a.sql":      "SELECT 1;",
		"sub/deep/V2__b.sql": "SELECT 2;",
	})
	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("递归扫描应找到 2 个，实际 %d", len(ms))
	}
	if got := []string{ms[0].Script, ms[1].Script}; got[0] != "sub/V1__a.sql" || got[1] != "sub/deep/V2__b.sql" {
		t.Fatalf("嵌套 script 路径应保留相对目录并使用正斜杠，实际 %#v", got)
	}
}

func TestScanBaselineAndCustomPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"B1__baseline.sql":  "CREATE TABLE base (id int);",
		"V1__versioned.sql": "CREATE TABLE versioned (id int);",
	})
	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("expected V1 and B1 to coexist, got %d: %#v", len(ms), ms)
	}
	var baseline *Migration
	for _, m := range ms {
		if m.Kind == KindBaseline {
			baseline = m
		}
	}
	if baseline == nil {
		t.Fatal("B1 was not scanned as a baseline migration")
	}
	if baseline.Type() != TypeSQLBaseline {
		t.Fatalf("baseline type = %q, want %q", baseline.Type(), TypeSQLBaseline)
	}
	if baseline.Description != "baseline" || baseline.Version.String() != "1" {
		t.Fatalf("baseline version/description = %s/%q", baseline.Version.String(), baseline.Description)
	}

	custom := DefaultNaming()
	custom.BaselinePrefix = "BB"
	writeFiles(t, dir, map[string]string{"BB2__other.sql": "SELECT 2;"})
	ms, err = (&Scanner{Naming: custom}).Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ms {
		if m.Kind == KindBaseline && m.Version.String() == "2" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom baseline prefix BB2 was not recognised")
	}
}

func TestScanRejectsBaselineWithoutVersion(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"B__bad.sql": "SELECT 1;"})
	s := &Scanner{Naming: DefaultNaming(), CollectInvalid: true}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Fatalf("invalid baseline should be ignored, got %#v", ms)
	}
	if len(s.InvalidNames) != 1 || !strings.Contains(s.InvalidNames[0].Message, "Invalid versioned migration name format: B__bad.sql") {
		t.Fatalf("invalid name error = %#v", s.InvalidNames)
	}
}

func TestScanVersionedAndUndoDuplicateRules(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"V1__a.sql": "SELECT 1;",
		"U1__b.sql": "SELECT 2;",
	})
	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("V1 and U1 must coexist: %v", err)
	}
	if len(ms) != 2 || ms[0].Kind != KindVersioned || ms[1].Kind != KindUndo {
		t.Fatalf("sort order = %#v", ms)
	}

	writeFiles(t, dir, map[string]string{"U1__c.sql": "SELECT 3;"})
	if _, err := s.Scan(dir); err == nil || !strings.Contains(err.Error(), "Found more than one migration with version 1") {
		t.Fatalf("duplicate U1 error = %v", err)
	}
}

func TestScanDuplicateBaselineError(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"B1__a.sql": "SELECT 1;",
		"B1__b.sql": "SELECT 2;",
	})
	_, err := (&Scanner{Naming: DefaultNaming()}).Scan(dir)
	if err == nil || !strings.Contains(err.Error(), "Found more than one migration with version 1") || !strings.Contains(err.Error(), "SQL_BASELINE") {
		t.Fatalf("duplicate baseline error = %v", err)
	}
}

func TestCompareResolvedValuesOfficialSemantics(t *testing.T) {
	v1, _ := ParseVersion("1")
	v2, _ := ParseVersion("2")
	cases := []struct {
		name         string
		aKind        MigrationKind
		aVersion     Version
		aDescription string
		bKind        MigrationKind
		bVersion     Version
		bDescription string
		want         int
	}{
		{"versioned before repeatable", KindVersioned, v1, "a", KindRepeatable, Version{}, "a", -1},
		{"repeatable after versioned", KindRepeatable, Version{}, "a", KindVersioned, v1, "a", 1},
		{"undo after versioned", KindVersioned, v1, "a", KindUndo, v1, "a", -1},
		{"undo before later version", KindUndo, v1, "a", KindVersioned, v2, "a", -1},
		{"repeatable description order", KindRepeatable, Version{}, "a", KindRepeatable, Version{}, "b", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareResolvedValues(tc.aKind, tc.aVersion, tc.aDescription, tc.bKind, tc.bVersion, tc.bDescription); got != tc.want {
				t.Fatalf("comparison = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestScanUndo(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"V1__create.sql":     "CREATE TABLE a (id int);",
		"U1__drop_table.sql": "DROP TABLE a;",
	})
	s := &Scanner{Naming: DefaultNaming()}
	ms, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var undoFound bool
	for _, m := range ms {
		if m.Kind == KindUndo {
			undoFound = true
			if m.Version.String() != "1" {
				t.Errorf("undo 版本应为 1，实际 %s", m.Version.String())
			}
		}
	}
	if !undoFound {
		t.Error("未识别到 U 前缀的撤销脚本")
	}
}

func TestVersionRawAndNullableSemantics(t *testing.T) {
	v := MustParseVersion("1_2")
	if got := v.String(); got != "1.2" {
		t.Fatalf("display = %q, want 1.2", got)
	}
	if got := v.RawVersion(); got != "1_2" {
		t.Fatalf("raw = %q, want 1_2", got)
	}
	if got, ok := EmptyVersion().GetVersionOrNull(); ok || got != "" {
		t.Fatalf("EMPTY getVersion = (%q,%v), want (\"\",false)", got, ok)
	}
	if got := ParseVersionNullable(nil); !got.IsEmpty() {
		t.Fatalf("fromVersion(null) = %s, want EMPTY", got)
	}
	if _, err := ParseVersion("bad"); err == nil || err.Error() != "Version may only contain 0..9 and . (dot). Invalid version: bad" {
		t.Fatalf("invalid version error = %v", err)
	}
}
