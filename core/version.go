package core

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// MigrationType 常量与 Flyway 历史表 type 字段取值对齐。
const (
	TypeSQLMigration = "SQL"
	TypeSQLBaseline  = "SQL_BASELINE"
	TypeJDBCBaseline = "JDBC_BASELINE"
	TypeSQLUndo      = "SQL_UNDO"
	TypeBaseline     = "BASELINE"
	TypeSchema       = "SCHEMA"
	TypeDelete       = "DELETE"
)

// MigrationKind 区分版本化脚本、可重复脚本、撤销脚本与基线。
type MigrationKind int

const (
	KindVersioned MigrationKind = iota
	KindRepeatable
	KindUndo
	KindBaseline
)

// Migration 表示一个待执行或已执行的迁移单元。
type Migration struct {
	Kind        MigrationKind
	Version     Version
	Description string
	Script      string // 原始文件名，如 V1__init.sql
	Path        string // 源路径
	RawVersion  string // 迁移文件名中的原始版本文本，对应 MigrationVersion.getRawVersion()
	Checksum    int64  // CRC32 无符号值，以 int64 承载以匹配 Flyway 的 long
	// EquivalentChecksum is the raw-resource checksum for repeatable migrations
	// when the resolved checksum uses placeholder replacement.
	EquivalentChecksum *int64
	SQL                []string // 拆分后的可执行语句（兼容旧 API，由 Statements 派生）
	// Statements preserves Flyway parser metadata for each statement, including
	// line/column, transaction capability, delimiter and PostgreSQL COPY data.
	Statements []ParsedStatement
	// NonTransactional marks migrations whose statements cannot run inside a
	// transaction, such as PostgreSQL CREATE INDEX CONCURRENTLY.
	NonTransactional bool
	Encoding         string

	// Script-level metadata loaded from <migration>.sql.conf. Nil means that the
	// corresponding global configuration value applies.
	ExecuteInTransaction    *bool
	PlaceholderReplacement  *bool
	ShouldExecuteExpression string

	// PlaceholderError is deferred until execution. Flyway can scan/info
	// unresolved placeholder resources; only migrate/validate execution fails.
	PlaceholderError string
}

// Repeatable 判断是否为可重复迁移（R__ 前缀）。
func (m *Migration) Repeatable() bool { return m.Kind == KindRepeatable }

// Type 返回写入历史表的 type 值。
func (m *Migration) Type() string {
	switch m.Kind {
	case KindUndo:
		return TypeSQLUndo
	case KindBaseline:
		return TypeSQLBaseline
	default:
		return TypeSQLMigration
	}
}

func (m *Migration) String() string {
	if m.Repeatable() {
		return "R__" + m.Description
	}
	return m.Version.String()
}

// Version 复刻 Flyway 的 MigrationVersion。
//
// 关键规则（依据官方 MigrationVersion.java）：
//   - 下划线先替换为点：1_2 等价于 1.2
//   - 分裂正则 "\\.(?=\\d)"：仅当下一个字符是数字时才在该点处分割，
//     因此 "1..2" 不会被切成三段，而是原样交给 BigInteger 解析并报错
//   - 使用 BigInteger，支持超出 int64 的大版本号
//   - 尾部零段被裁剪：1.0.0 -> [1]，因此 1 == 1.0 == 1.0.0
type Version struct {
	parts   []*big.Int
	display string
	raw     string
	predef  int // 0=正常, 1=EMPTY, 2=LATEST, 3=CURRENT, 4=NEXT
}

const (
	predefNone    = 0
	predefEmpty   = 1
	predefLatest  = 2
	predefCurrent = 3
	predefNext    = 4
)

// EmptyVersion 对应 MigrationVersion.EMPTY。
func EmptyVersion() Version {
	return Version{display: "<< Empty Schema >>", raw: "<< Empty Schema >>", predef: predefEmpty}
}

// CurrentVersion 对应 MigrationVersion.CURRENT。
func CurrentVersion() Version {
	return Version{display: "<< Current Version >>", raw: "<< Current Version >>", predef: predefCurrent}
}

// NextVersion 对应 MigrationVersion.NEXT。
func NextVersion() Version {
	return Version{display: "<< Next Version >>", raw: "<< Next Version >>", predef: predefNext}
}

// LatestVersion 对应 MigrationVersion.LATEST。
func LatestVersion() Version {
	return Version{display: "<< Latest Version >>", raw: "<< Latest Version >>", predef: predefLatest}
}

// splitVersionParts 等价于 Java 的 SPLIT_REGEX = "\\.(?=\\d)"：
// 仅当点号后面紧跟一个数字时才在点号处分割，且 split 默认丢弃尾部空串。
// Go 的 RE2 不支持 lookahead，故手工实现以保证语义逐位一致。
func splitVersionParts(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	// Java String.split 会丢弃末尾的空串
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ParseVersion 解析版本号。
func ParseVersion(s string) (Version, error) {
	// 官方 fromVersion 只识别关键字和 Long.MAX_VALUE，不识别历史表中的
	// "<< Latest Version >>" 等展示值；若从配置或历史表读到这些值必须报错。
	switch {
	case strings.EqualFold(s, "current"):
		return CurrentVersion(), nil
	case strings.EqualFold(s, "next"):
		return NextVersion(), nil
	case strings.EqualFold(s, "latest"), s == "9223372036854775807":
		return LatestVersion(), nil
	}
	if s == "" {
		return Version{}, fmt.Errorf("Version may only contain 0..9 and . (dot). Invalid version: %s", s)
	}

	normalized := strings.ReplaceAll(s, "_", ".")
	parts := make([]*big.Int, 0, 4)
	for _, seg := range splitVersionParts(normalized) {
		n, ok := new(big.Int).SetString(seg, 10)
		if !ok || !isPlainInteger(seg) {
			return Version{}, fmt.Errorf("Version may only contain 0..9 and . (dot). Invalid version: %s", s)
		}
		parts = append(parts, n)
	}
	// 裁剪尾部零段
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i].Sign() != 0 {
			break
		}
		parts = parts[:i]
	}
	return Version{parts: parts, display: normalized, raw: s}, nil
}

// ParseVersionNullable mirrors MigrationVersion.fromVersion(null): a nil input
// maps to EMPTY, while any non-nil input uses the normal validation path.
func ParseVersionNullable(s *string) Version {
	if s == nil {
		return EmptyVersion()
	}
	v, err := ParseVersion(*s)
	if err != nil {
		// Callers that need the FlywayException must use ParseVersion. This helper
		// exists only for the null-compatible path used by configuration models.
		return Version{}
	}
	return v
}

// isPlainInteger 复刻 BigInteger(String) 接受的十进制形式：可选正负号 + 数字。
func isPlainInteger(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '+' || s[0] == '-' {
		start = 1
	}
	if start == len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// MustParseVersion 用于常量与测试。
func MustParseVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Compare 与 Java compareTo 语义一致。
func (v Version) Compare(other Version) int {
	// 直接照搬 MigrationVersion.compareTo 的非对称分支顺序。
	if v.predef == predefEmpty {
		if other.predef == predefEmpty {
			return 0
		}
		return -1
	}
	if v.predef == predefCurrent {
		if other.predef == predefCurrent {
			return 0
		}
		return -1
	}
	if v.predef == predefLatest {
		if other.predef == predefLatest {
			return 0
		}
		return 1
	}
	if other.predef == predefEmpty {
		return 1
	}
	if other.predef == predefCurrent {
		return 1
	}
	if other.predef == predefNext {
		return -1
	}
	if other.predef == predefLatest {
		return -1
	}
	max := len(v.parts)
	if len(other.parts) > max {
		max = len(other.parts)
	}
	for i := 0; i < max; i++ {
		var a, b *big.Int = big.NewInt(0), big.NewInt(0)
		if i < len(v.parts) {
			a = v.parts[i]
		}
		if i < len(other.parts) {
			b = other.parts[i]
		}
		if c := a.Cmp(b); c != 0 {
			return c
		}
	}
	return 0
}

// String 返回展示用文本（与 Java displayText 一致）。
func (v Version) String() string { return v.display }

// RawVersion corresponds to MigrationVersion.getRawVersion(). Flyway keeps the
// text before underscore normalization and prints it in JSON info output.
func (v Version) RawVersion() string {
	if v.raw != "" {
		return v.raw
	}
	return v.display
}

// GetVersionOrNull returns the Java getVersion() result, including the null
// distinction that Go cannot represent with a plain string.
func (v Version) GetVersionOrNull() (string, bool) {
	if v.IsEmpty() {
		return "", false
	}
	return v.GetVersion(), true
}

// GetVersion 对应 MigrationVersion.getVersion()：空版本返回空串表示 SQL NULL，
// LATEST 返回 Long.MAX_VALUE，其余返回 displayText。
func (v Version) GetVersion() string {
	if v.IsEmpty() {
		return ""
	}
	if v.predef == predefLatest {
		return "9223372036854775807"
	}
	return v.display
}

// Name 对应 MigrationVersion.getName()。
func (v Version) Name() string {
	if v.IsEmpty() {
		return ""
	}
	switch v.predef {
	case predefLatest:
		return "latest"
	case predefCurrent:
		return "current"
	case predefNext:
		return "next"
	default:
		return v.display
	}
}

// Canonical returns a normalized key for equivalent versions, e.g. 1, 1.0, 1.0.0 -> 1.
func (v Version) Canonical() string {
	if v.IsZero() {
		return "0"
	}
	if v.predef != predefNone {
		return v.display
	}
	if len(v.parts) == 0 {
		return ""
	}
	parts := make([]string, len(v.parts))
	for i, p := range v.parts {
		parts[i] = p.String()
	}
	return strings.Join(parts, ".")
}

// IsZero 表示未设置（空版本）。
func (v Version) IsZero() bool {
	return v.predef == predefNone && len(v.parts) == 0 && v.display == ""
}

// IsEmpty 对应 MigrationVersion.EMPTY。
func (v Version) IsEmpty() bool { return v.predef == predefEmpty }

// IsLatest 对应 MigrationVersion.LATEST。
func (v Version) IsLatest() bool { return v.predef == predefLatest }

// SortVersions 升序排序。
func SortVersions(versions []Version) {
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].Compare(versions[j]) < 0
	})
}
