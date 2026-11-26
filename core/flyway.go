package core

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NamingConfig 对应 Flyway 的 sqlMigrationPrefix / undoSqlMigrationPrefix /
// repeatableSqlMigrationPrefix / sqlMigrationSeparator / sqlMigrationSuffixes。
type NamingConfig struct {
	VersionedPrefix  string
	UndoPrefix       string
	RepeatablePrefix string
	BaselinePrefix   string
	Separator        string
	Suffixes         []string
	Encoding         string
}

// DefaultNaming 与 Flyway 默认值一致。
func DefaultNaming() NamingConfig {
	return NamingConfig{
		VersionedPrefix:  "V",
		UndoPrefix:       "U",
		RepeatablePrefix: "R",
		BaselinePrefix:   "B",
		Separator:        "__",
		Suffixes:         []string{".sql"},
		Encoding:         "UTF-8",
	}
}

// resolvedMigration 表示从磁盘扫描出来、尚未计算校验和与语句的原始条目。
type InvalidMigrationName struct {
	Script   string
	KindHint string
	Message  string
}

type resolvedMigration struct {
	Kind        MigrationKind
	Version     Version
	Description string
	Script      string
	Path        string
	RawVersion  string
	Raw         []byte
}

// Scanner 从目录扫描并解析迁移脚本。
type Scanner struct {
	Naming                 NamingConfig
	Driver                 string
	DBVersion              int
	Mixed                  bool
	ExecuteInTransaction   bool
	PlaceholderReplacement bool
	CollectInvalid         bool
	Placeholders           map[string]string
	PlaceholderPrefix      string
	PlaceholderSuffix      string
	PlaceholderSeparator   string
	DetectEncoding         bool
	FailOnMissingLocations bool
	WorkingDirectory       string
	InvalidNames           []InvalidMigrationName
}

// Scan 递归扫描目录，返回按 Flyway 规则解析出的迁移列表（版本化升序 + 可重复脚本）。
// Location handling mirrors FileSystemScanner: missing or invalid locations are
// skipped by default, hidden directories are ignored, and unreadable files are
// skipped. The location may use the official filesystem: prefix.
func (s *Scanner) Scan(location string) ([]*Migration, error) {
	dir, err := filesystemLocationPath(location)
	if err != nil {
		return nil, err
	}
	status := directoryValidationResult(dir)
	if status != "valid" {
		hint := s.workingDirectoryHint(dir)
		if s.FailOnMissingLocations {
			return nil, fmt.Errorf("Failed to find filesystem location: %s (%s)%s", dir, status, hint)
		}
		return nil, nil
	}

	var resolved []resolvedMigration
	var walkErr error
	walkErr = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErrIn error) error {
		if walkErrIn != nil {
			if s.FailOnMissingLocations {
				return fmt.Errorf("Failed to find filesystem location: %s (unable to access folder)", path)
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != dir && isHiddenDirectory(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !s.hasValidSuffix(name) {
			return nil
		}
		if !isReadableEntry(path, d) {
			return nil
		}
		rm, perr := s.parseFileName(name, path)
		if perr != nil {
			if s.CollectInvalid {
				var kind string
				if strings.HasPrefix(name, s.Naming.RepeatablePrefix) {
					kind = s.Naming.RepeatablePrefix
				} else if strings.HasPrefix(name, s.Naming.VersionedPrefix) {
					kind = s.Naming.VersionedPrefix
				}
				s.InvalidNames = append(s.InvalidNames, InvalidMigrationName{Script: name, KindHint: kind, Message: perr.Error()})
			}
			// Flyway 对不符合命名规范的 .sql 文件默认直接忽略（不报错）
			return nil
		}
		// MigrationInfo.getScript() reports the resource path relative to its
		// filesystem location, always using forward slashes. Top-level scripts
		// are therefore just their filename, while nested resources retain the
		// subdirectory (for example sub/V1__init.sql).
		if rel, relErr := filepath.Rel(dir, path); relErr == nil {
			rm.Script = filepath.ToSlash(rel)
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			if os.IsPermission(rerr) {
				return nil
			}
			return fmt.Errorf("读取 %s 失败: %w", path, rerr)
		}
		rm.Raw = raw
		resolved = append(resolved, *rm)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	if err := s.checkDuplicates(resolved); err != nil {
		return nil, err
	}

	// 排序与官方 ResolvedMigrationComparator 完全一致。注意 comparator
	// 对同版本的非 undo 迁移返回 0，stable sort 保留扫描顺序。
	sort.SliceStable(resolved, func(i, j int) bool {
		return compareResolvedValues(resolved[i].Kind, resolved[i].Version, resolved[i].Description,
			resolved[j].Kind, resolved[j].Version, resolved[j].Description) < 0
	})

	migrations := make([]*Migration, 0, len(resolved))
	for _, r := range resolved {
		driver := strings.ToLower(s.Driver)
		if driver == "" {
			driver = "postgres"
		}
		executeInTransaction := s.ExecuteInTransaction
		placeholderReplacement := s.PlaceholderReplacement
		if s.Driver == "" {
			// Keep Scanner safe for callers constructing it directly. New() always
			// supplies the resolved Flyway configuration explicitly.
			executeInTransaction = true
			placeholderReplacement = true
		}
		md, merr := LoadSQLScriptMetadataWithEncoding(MigrationMetadataPath(r.Path), s.Placeholders,
			s.PlaceholderPrefix, s.PlaceholderSuffix, s.PlaceholderSeparator, s.Naming.Encoding)
		if merr != nil {
			return nil, fmt.Errorf("解析 %s 的脚本配置失败: %w", r.Script, merr)
		}
		placeholderReplacement = effectivePlaceholderReplacement(placeholderReplacement, md.PlaceholderReplacement)
		detectEncoding := s.DetectEncoding
		if md.Encoding != "" {
			detectEncoding = false
		}
		rawContent, decodeErr := DecodeScript(r.Raw, encodingForMetadata(s.Naming.Encoding, md.Encoding), detectEncoding)
		if decodeErr != nil {
			return nil, fmt.Errorf("Failed to read %s: %w", r.Script, decodeErr)
		}
		content := rawContent
		// 去除 BOM，与 Java 版读取行为对齐
		content = strings.TrimPrefix(content, "\uFEFF")
		replaced := content
		var placeholderErr error
		if placeholderReplacement {
			replaced, placeholderErr = ReplacePlaceholders(content, s.Placeholders, s.PlaceholderPrefix, s.PlaceholderSuffix, s.PlaceholderSeparator)
		}
		// Flyway resolves placeholders for execution, but SqlMigrationResolver
		// calculates a versioned checksum from the raw resource. Only repeatable
		// migrations use placeholder-replaced content in their resolved checksum.
		// An unresolved placeholder is deferred until execution: info/validate
		// can still scan and report the migration without executing its SQL.
		checksumSource := content
		if r.Kind == KindRepeatable && placeholderReplacement && placeholderErr == nil {
			checksumSource = replaced
		}
		executionContent := content
		if placeholderErr == nil {
			executionContent = replaced
		}
		parsed, serr := ParseSQLScript(executionContent, driver, r.Path, s.DBVersion)
		if serr != nil {
			return nil, fmt.Errorf("解析 %s 的语句失败: %w", r.Script, serr)
		}
		stmts := make([]string, 0, len(parsed))
		nonTransactional := false
		for _, st := range parsed {
			stmts = append(stmts, st.SQL)
			if !st.CanExecuteInTransaction {
				nonTransactional = true
			}
		}
		if err := validateMixedStatementKinds(parsed, s.Mixed, md.ExecuteInTransaction, executeInTransaction); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", r.Script, err)
		}
		var equivalentChecksum *int64
		if r.Kind == KindRepeatable {
			value := Checksum(rawContent)
			equivalentChecksum = &value
		}
		encoding := s.Naming.Encoding
		if md.Encoding != "" {
			encoding = md.Encoding
		}
		migrations = append(migrations, &Migration{
			Kind:                    r.Kind,
			Version:                 r.Version,
			Description:             r.Description,
			Script:                  r.Script,
			Path:                    r.Path,
			RawVersion:              r.RawVersion,
			Checksum:                Checksum(checksumSource),
			EquivalentChecksum:      equivalentChecksum,
			SQL:                     stmts,
			Statements:              parsed,
			NonTransactional:        nonTransactional,
			Encoding:                encoding,
			ExecuteInTransaction:    md.ExecuteInTransaction,
			PlaceholderReplacement:  md.PlaceholderReplacement,
			ShouldExecuteExpression: md.ShouldExecuteExpression,
			PlaceholderError: func() string {
				if placeholderErr != nil {
					return placeholderErr.Error()
				}
				return ""
			}(),
		})
	}
	return migrations, nil
}

// containsConcurrently reports whether any statement contains PostgreSQL's
// CREATE INDEX CONCURRENTLY, which cannot execute inside a transaction block.
func containsConcurrently(stmts []string) bool {
	for _, stmt := range stmts {
		upper := strings.ToUpper(stmt)
		if strings.Contains(upper, "CREATE") && strings.Contains(upper, "INDEX") && strings.Contains(upper, "CONCURRENTLY") {
			return true
		}
	}
	return false
}

func (s *Scanner) hasValidSuffix(name string) bool {
	for _, suf := range s.Naming.Suffixes {
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suf)) {
			return true
		}
	}
	return false
}

// migrationPrefix 保存 CLI 配置中的一个资源前缀及其语义类型。
type migrationPrefix struct {
	prefix string
	kind   MigrationKind
}

// migrationPrefixes 对应 ResourceNameParser.populatePrefixes：前缀按长度降序，
// 长度相同时保留 provider 的注册顺序。这样可以正确处理 B / BB 等重叠前缀。
func (s *Scanner) migrationPrefixes() []migrationPrefix {
	prefixes := []migrationPrefix{
		{prefix: s.Naming.VersionedPrefix, kind: KindVersioned},
		{prefix: s.Naming.RepeatablePrefix, kind: KindRepeatable},
		{prefix: s.Naming.UndoPrefix, kind: KindUndo},
		{prefix: s.Naming.BaselinePrefix, kind: KindBaseline},
	}
	out := make([]migrationPrefix, 0, len(prefixes))
	for _, entry := range prefixes {
		if entry.prefix != "" {
			out = append(out, entry)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].prefix) > len(out[j].prefix)
	})
	return out
}

// parseFileName 复刻 Flyway 的 ResourceNameParser 逻辑。
func (s *Scanner) parseFileName(name, path string) (*resolvedMigration, error) {
	stem := name
	suffix := ""
	for _, suf := range s.Naming.Suffixes {
		if strings.HasSuffix(strings.ToLower(stem), strings.ToLower(suf)) {
			stem = stem[:len(stem)-len(suf)]
			suffix = name[len(name)-len(suf):]
			break
		}
	}

	var matched *migrationPrefix
	for _, entry := range s.migrationPrefixes() {
		if strings.HasPrefix(stem, entry.prefix) {
			candidate := entry
			matched = &candidate
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("Unrecognised migration name format: %s", name)
	}

	remainder := stem[len(matched.prefix):]
	sepIndex := strings.Index(remainder, s.Naming.Separator)
	left, right := remainder, ""
	if sepIndex >= 0 {
		left = remainder[:sepIndex]
		right = remainder[sepIndex+len(s.Naming.Separator):]
	}
	exampleDescription := right
	if exampleDescription == "" {
		exampleDescription = "description"
	}

	if matched.kind == KindRepeatable {
		if left != "" {
			return nil, fmt.Errorf("Invalid repeatable migration / callback name format: %s (It cannot contain a version and should look like this: %s%s%s%s)",
				name, matched.prefix, s.Naming.Separator, exampleDescription, suffix)
		}
		return &resolvedMigration{
			Kind:        KindRepeatable,
			Description: normalizeDescription(right),
			Script:      name,
			Path:        path,
		}, nil
	}

	if left == "" {
		return nil, fmt.Errorf("Invalid versioned migration name format: %s (It must contain a version and should look like this: %s1.2%s%s%s)",
			name, matched.prefix, s.Naming.Separator, exampleDescription, suffix)
	}
	v, err := ParseVersion(left)
	if err != nil {
		return nil, fmt.Errorf("Invalid versioned migration name format: %s (could not recognise version number %s)", name, left)
	}
	return &resolvedMigration{
		Kind:        matched.kind,
		Version:     v,
		Description: normalizeDescription(right),
		Script:      name,
		Path:        path,
		RawVersion:  left,
	}, nil
}

// ReplacePlaceholders corresponds to PlaceholderReplacingReader with the default
// prefix ${ and suffix }. Placeholder names are case-insensitive; unresolved
// placeholders fail exactly like Flyway.
func ReplacePlaceholders(content string, values map[string]string, prefix, suffix, separator string) (string, error) {
	if prefix == "" {
		prefix = "${"
	}
	if suffix == "" {
		suffix = "}"
	}
	if separator == "" {
		separator = ":"
	}
	resolved := make(map[string]string, len(values))
	for k, v := range values {
		resolved[strings.ToLower(k)] = v
	}

	var b strings.Builder
	for {
		start := strings.Index(content, prefix)
		if start < 0 {
			b.WriteString(content)
			return b.String(), nil
		}
		b.WriteString(content[:start])
		rest := content[start+len(prefix):]
		end := strings.Index(rest, suffix)
		name := rest
		terminated := false
		if end >= 0 {
			name = rest[:end]
			terminated = true
		}

		value, ok := placeholderValue(resolved, name, separator)
		if !ok {
			canonical := prefix + name + suffix
			if strings.HasPrefix(name, "flyway"+separator) {
				return "", fmt.Errorf("Failed to populate value for default placeholder: %s", canonical)
			}
			return "", fmt.Errorf("No value provided for placeholder: %s.  Check your configuration!", canonical)
		}
		b.WriteString(value)
		if !terminated {
			return b.String(), nil
		}
		content = rest[end+len(suffix):]
	}
}

// placeholderValue resolves a placeholder body. A configured name wins, including
// an explicit empty value. Otherwise name<separator>default uses the default
// when the name is absent. Built-in flyway<separator> placeholders are not
// defaults; the caller reports those separately.
func placeholderValue(resolved map[string]string, name, separator string) (string, bool) {
	if value, ok := resolved[strings.ToLower(name)]; ok {
		return value, true
	}
	if strings.HasPrefix(name, "flyway"+separator) {
		return "", false
	}
	idx := strings.Index(name, separator)
	if idx < 0 {
		return "", false
	}
	actual := name[:idx]
	if value, ok := resolved[strings.ToLower(actual)]; ok {
		return value, true
	}
	return name[idx+len(separator):], true
}

func encodingForMetadata(global, override string) string {
	if override != "" {
		return override
	}
	return global
}

// normalizeDescription 与 ResourceNameParser 一致：只把下划线替换成空格。
// 短横线、首尾空白及其他字符均原样保留。
func normalizeDescription(desc string) string {
	return strings.ReplaceAll(desc, "_", " ")
}

func migrationKindHasVersion(kind MigrationKind) bool { return kind != KindRepeatable }

func migrationKindIsUndo(kind MigrationKind) bool { return kind == KindUndo }

func migrationTypeForKind(kind MigrationKind) string {
	switch kind {
	case KindUndo:
		return TypeSQLUndo
	case KindBaseline:
		return TypeSQLBaseline
	default:
		return TypeSQLMigration
	}
}

// compareResolvedValues 逐行复刻 ResolvedMigrationComparator.compare。
func compareResolvedValues(aKind MigrationKind, aVersion Version, aDescription string,
	bKind MigrationKind, bVersion Version, bDescription string) int {
	aVersioned := migrationKindHasVersion(aKind)
	bVersioned := migrationKindHasVersion(bKind)
	if aVersioned && bVersioned {
		v := aVersion.Compare(bVersion)
		if v == 0 {
			if migrationKindIsUndo(aKind) && migrationKindIsUndo(bKind) {
				return 0
			}
			if migrationKindIsUndo(aKind) {
				return 1
			}
			if migrationKindIsUndo(bKind) {
				return -1
			}
		}
		return v
	}
	if aVersioned {
		return -1
	}
	if bVersioned {
		return 1
	}
	return strings.Compare(aDescription, bDescription)
}

// canCompareResolvedKinds 对应 BaselineResolvedMigration.canCompareWith：
// baseline 只能与 baseline 比较，其他 resolved migration 默认均可比较。
func canCompareResolvedKinds(a, b MigrationKind) bool {
	if a == KindBaseline || b == KindBaseline {
		return a == KindBaseline && b == KindBaseline
	}
	return true
}

func resolvedDuplicateError(aKind MigrationKind, aVersion Version, aDescription, aScript, aPath string,
	bKind MigrationKind, bVersion Version, bDescription, bScript, bPath string) error {
	aLocation, bLocation := aPath, bPath
	if aLocation == "" {
		aLocation = aScript
	}
	if bLocation == "" {
		bLocation = bScript
	}
	if migrationKindHasVersion(aKind) {
		return fmt.Errorf("Found more than one migration with version %s\nOffenders:\n-> %s (%s)\n-> %s (%s)",
			aVersion, aLocation, migrationTypeForKind(aKind), bLocation, migrationTypeForKind(bKind))
	}
	return fmt.Errorf("Found more than one repeatable migration with description %s\nOffenders:\n-> %s (%s)\n-> %s (%s)",
		aDescription, aLocation, migrationTypeForKind(aKind), bLocation, migrationTypeForKind(bKind))
}

func (s *Scanner) checkDuplicates(items []resolvedMigration) error {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			a, b := items[i], items[j]
			if !canCompareResolvedKinds(a.Kind, b.Kind) ||
				compareResolvedValues(a.Kind, a.Version, a.Description, b.Kind, b.Version, b.Description) != 0 {
				continue
			}
			return resolvedDuplicateError(a.Kind, a.Version, a.Description, a.Script, a.Path,
				b.Kind, b.Version, b.Description, b.Script, b.Path)
		}
	}
	return nil
}

// Checksum 复刻 Flyway 的 CRC32 校验和算法。
//
// 依据 flyway-core/ChecksumCalculator.calculateChecksumForResource：
//
//	crc32 := new(CRC32)
//	line := readLine()            // BufferedReader.readLine()
//	line = FilterBomFromString(line)
//	do {
//	    crc32.update(line.getBytes(UTF_8))
//	} while ((line = readLine()) != null)
//	return (int) crc32.getValue()
//
// 三个必须精确复刻的细节：
//  1. 逐行喂入 CRC32，**不追加任何换行符**，因此结果与换行风格无关；
//  2. 仅对**第一行**做 BOM 剥离（FilterBomFromString）；
//  3. 返回值是 int（有符号 32 位），通过 (int) 强转，历史上可能为负数。
//
// Go 的 hash/crc32.ChecksumIEEE 与 java.util.zip.CRC32 同为 IEEE 802.3
// 多项式、初值 0、末值异或 0，结果一致。
func Checksum(content string) int64 {
	lines := splitLines(content)
	crc := crc32.NewIEEE()
	first := true
	for _, line := range lines {
		if first {
			line = filterBOM(line)
			first = false
		}
		crc.Write([]byte(line))
	}
	// 与 (int) crc32.getValue() 对齐：截断为有符号 32 位
	return int64(int32(crc.Sum32()))
}

// splitLines 复刻 java.io.BufferedReader.readLine() 的分行语义：
// 以 \n、\r、\r\n 为界，末尾换行不产生额外的空行。
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			lines = append(lines, s[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// filterBOM 对应 Flyway 的 BomFilter.FilterBomFromString。
func filterBOM(s string) string {
	if strings.HasPrefix(s, "\uFEFF") {
		return s[len("\uFEFF"):]
	}
	if strings.HasPrefix(s, "\xEF\xBB\xBF") {
		return s[3:]
	}
	return s
}

var delimiterRe = regexp.MustCompile(`(?i)^\s*DELIMITER\s+(\S+)\s*$`)

// SplitStatements 拆分 SQL 语句，支持：
//   - 标准分号分隔
//   - MySQL 的 DELIMITER $$ 指令
//   - PostgreSQL 的 $$ / $tag$ 美元引用块
//   - GO 批处理分隔符（SQL Server 风格，兼容忽略）
//   - 单/双引号与反引号字符串、-- 与 /* */ 注释
func SplitStatements(content string) ([]string, error) {
	var (
		stmts        []string
		cur          strings.Builder
		delimiter    = ";"
		i            = 0
		n            = len(content)
		inRoutine    bool
		routineDepth int
	)

	flush := func() {
		stmt := strings.TrimSpace(cur.String())
		if strings.HasSuffix(stmt, delimiter) {
			stmt = strings.TrimSpace(strings.TrimSuffix(stmt, delimiter))
		}
		if strings.HasSuffix(stmt, ";") {
			stmt = strings.TrimSpace(strings.TrimSuffix(stmt, ";"))
		}
		stmt = trimLeadingSQLComments(stmt)
		if stmt != "" && !isOnlyComments(stmt) {
			stmts = append(stmts, stmt)
		}
		cur.Reset()
		inRoutine = false
		routineDepth = 0
	}

	for i < n {
		// 行首处理 DELIMITER 指令与 GO 分隔符
		if isLineStart(content, i) {
			lineEnd := strings.IndexByte(content[i:], '\n')
			var line string
			var next int
			if lineEnd < 0 {
				line = content[i:]
				next = n
			} else {
				line = content[i : i+lineEnd]
				next = i + lineEnd + 1
			}
			if m := delimiterRe.FindStringSubmatch(line); m != nil {
				flush()
				delimiter = m[1]
				i = next
				continue
			}
			if cur.Len() == 0 || isBlankLine(cur.String()) {
				if strings.EqualFold(strings.TrimSpace(line), "GO") {
					flush()
					i = next
					continue
				}
			}
		}

		c := content[i]

		// MySQL 存储程序：避免 BEGIN...END 内部的分号提前拆分。
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			j := i
			for j < n {
				ch := content[j]
				if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
					j++
					continue
				}
				break
			}
			word := content[i:j]
			trimmed := strings.TrimSpace(cur.String())
			atStatementStart := trimmed == "" || isOnlyComments(trimmed)
			if !inRoutine && strings.EqualFold(word, "CREATE") && atStatementStart && routineRe.MatchString(content[i:]) {
				inRoutine = true
			}
			if inRoutine {
				switch {
				case strings.EqualFold(word, "BEGIN"):
					routineDepth++
				case strings.EqualFold(word, "END"):
					if routineDepth > 0 {
						routineDepth--
					}
				case strings.EqualFold(word, "CASE"):
					routineDepth++
				case strings.EqualFold(word, "IF"), strings.EqualFold(word, "LOOP"),
					strings.EqualFold(word, "REPEAT"), strings.EqualFold(word, "WHILE"):
					low := strings.ToLower(trimmed)
					if atStatementStart || strings.HasSuffix(low, ";") || strings.HasSuffix(low, "begin") ||
						strings.HasSuffix(low, "then") || strings.HasSuffix(low, "else") {
						routineDepth++
					}
				}
			}
			cur.WriteString(word)
			i = j
			continue
		}

		// 行注释 --
		if c == '-' && i+1 < n && content[i+1] == '-' {
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				cur.WriteString(content[i:])
				i = n
			} else {
				cur.WriteString(content[i : i+end])
				i += end
			}
			continue
		}

		// 块注释 /* */，官方嵌套深度按 /* 与 */ 计数
		if c == '/' && i+1 < n && content[i+1] == '*' {
			depth := 0
			end := -1
			for j := i; j+1 < n; j++ {
				if content[j] == '/' && content[j+1] == '*' {
					depth++
					j++
				} else if content[j] == '*' && content[j+1] == '/' {
					depth--
					if depth == 0 {
						end = j + 1
						break
					}
					j++
				}
			}
			if end < 0 {
				// 官方将未闭合嵌套注释连同后续内容归入当前语句
				flush()
				i = n
				continue
			}
			cur.WriteString(content[i : end+1])
			i = end + 1
			continue
		}

		// 引号字符串
		if c == '\'' || c == '"' || c == '`' {
			j := i + 1
			cur.WriteByte(c)
			for j < n {
				ch := content[j]
				cur.WriteByte(ch)
				if ch == '\\' && c != '`' && j+1 < n {
					cur.WriteByte(content[j+1])
					j += 2
					continue
				}
				if ch == c {
					// 连续两个引号表示转义（SQL 标准）
					if j+1 < n && content[j+1] == c {
						cur.WriteByte(c)
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			i = j
			continue
		}

		// 自定义分隔符优先，避免 MySQL DELIMITER $$ 被误识别为 PG dollar-quote
		if delimiter != ";" && strings.HasPrefix(content[i:], delimiter) {
			if inRoutine && routineDepth > 0 {
				cur.WriteString(delimiter)
				i += len(delimiter)
				continue
			}
			flush()
			i += len(delimiter)
			continue
		}

		// 美元引用块 $tag$ ... $tag$
		if c == '$' {
			if tag, ok := matchDollarTag(content, i); ok {
				end := strings.Index(content[i+len(tag):], tag)
				if end >= 0 {
					endIdx := i + len(tag) + end + len(tag)
					cur.WriteString(content[i:endIdx])
					i = endIdx
					continue
				}
			}
		}

		// 分隔符匹配
		if strings.HasPrefix(content[i:], delimiter) {
			if inRoutine && routineDepth > 0 {
				cur.WriteString(delimiter)
				i += len(delimiter)
				continue
			}
			flush()
			i += len(delimiter)
			continue
		}

		// 普通字符
		cur.WriteByte(c)
		i++
	}
	flush()
	return stmts, nil
}

func isLineStart(s string, i int) bool {
	if i == 0 {
		return true
	}
	// 向前回溯空白，遇到换行即视为行首
	for j := i - 1; j >= 0; j-- {
		switch s[j] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

func isBlankLine(s string) bool { return strings.TrimSpace(s) == "" }

// trimLeadingSQLComments removes comments that precede the first executable
// token. Flyway's parser treats them as part of the recorded statement, but the
// public SplitStatements API historically returns only executable SQL and one
// regression test relies on this normalization.
func trimLeadingSQLComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			if n := strings.IndexByte(s, '\n'); n >= 0 {
				s = s[n+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			depth := 0
			end := -1
			for i := 0; i+1 < len(s); i++ {
				switch s[i : i+2] {
				case "/*":
					depth++
					i++
				case "*/":
					depth--
					i++
					if depth == 0 {
						end = i + 1
						break
					}
				}
				if end >= 0 {
					break
				}
			}
			if end >= 0 {
				s = s[end:]
				continue
			}
			return ""
		}
		return s
	}
}

func isOnlyComments(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") || strings.HasPrefix(t, "/*") ||
			strings.HasPrefix(t, "*") || strings.HasPrefix(t, "*/") {
			continue
		}
		return false
	}
	return true
}

var routineRe = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:DEFINER\s*=\s*\S+\s+)?(?:AGGREGATE\s+)?(?:FUNCTION|PROCEDURE|TRIGGER)\b`)

func matchDollarTag(s string, i int) (string, bool) {
	if i >= len(s) || s[i] != '$' {
		return "", false
	}
	j := i + 1
	for j < len(s) {
		c := s[j]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			j++
			continue
		}
		break
	}
	if j > i+1 && j < len(s) && s[j] == '$' {
		return s[i : j+1], true
	}
	if i+1 < len(s) && s[i+1] == '$' {
		return "$$", true
	}
	return "", false
}

func filesystemLocationPath(location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", fmt.Errorf("Unknown prefix for location: %s", location)
	}
	// Windows drive letters contain a colon but are not location prefixes.
	if len(location) >= 2 && location[1] == ':' && ((location[0] >= 'a' && location[0] <= 'z') || (location[0] >= 'A' && location[0] <= 'Z')) {
		return location, nil
	}
	if i := strings.IndexByte(location, ':'); i >= 0 {
		prefix := location[:i+1]
		path := location[i+1:]
		switch strings.ToLower(prefix) {
		case "filesystem:", "classpath:":
			return path, nil
		default:
			return "", fmt.Errorf("Unknown prefix for location (should be one of classpath:, filesystem:): %s", location)
		}
	}
	return location, nil
}

func directoryValidationResult(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "not found"
	}
	if !info.IsDir() {
		return "not a directory"
	}
	if isUnreadableMode(info) {
		return "not readable"
	}
	return "valid"
}

func isReadableEntry(path string, entry os.DirEntry) bool {
	info, err := entry.Info()
	if err != nil {
		return false
	}
	return !isUnreadableMode(info)
}

func isUnreadableMode(info os.FileInfo) bool {
	// Windows does not expose POSIX permission bits reliably; only treat a
	// definitely unreadable file as unreadable and let ReadFile surface errors.
	return info.Mode().Perm()&0444 == 0
}

func (s *Scanner) workingDirectoryHint(dir string) string {
	if filepath.IsAbs(dir) {
		return ""
	}
	abs, _ := filepath.Abs(dir)
	workingDirectory := s.WorkingDirectory
	if workingDirectory == "" {
		workingDirectory, _ = os.Getwd()
	}
	return fmt.Sprintf(" Resolved to '%s'. If this is not where your migrations are, check that your working directory is set correctly (working directory: '%s').", abs, workingDirectory)
}
