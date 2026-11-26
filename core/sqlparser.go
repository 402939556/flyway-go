package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ParsedStatement is the Go equivalent of Flyway's ParsedSqlStatement plus the
// PostgreSQL COPY payload. SQL is the executable statement without its
// terminating delimiter.
type ParsedStatement struct {
	SQL                     string
	Line                    int
	Col                     int
	Delimiter               string
	CanExecuteInTransaction bool
	Batchable               bool
	Copy                    bool
	CopyData                string
}

type sqlTokenType int

const (
	sqlTokenKeyword sqlTokenType = iota
	sqlTokenIdentifier
	sqlTokenNumeric
	sqlTokenString
	sqlTokenComment
	sqlTokenCommentDirective
	sqlTokenDelimiter
	sqlTokenNewDelimiter
	sqlTokenSymbol
	sqlTokenBlankLines
	sqlTokenEOF
)

type sqlToken struct {
	typ         sqlTokenType
	start       int
	end         int
	line        int
	col         int
	text        string
	parensDepth int
}

type sqlParser struct {
	driver        string
	dbVersion     int
	path          string
	content       []rune
	pos           int
	line          int
	col           int
	delimiter     string
	parensDepth   int
	blockDepth    int
	blockStack    []string
	statementType string
}

var (
	pgCopyFromStdinRe = regexp.MustCompile(`(?i)^COPY( .*)? FROM STDIN`)
	pgNonTxRe         = regexp.MustCompile(`(?i)^((CREATE|DROP) (DATABASE|TABLESPACE|SUBSCRIPTION)|ALTER SYSTEM|(CREATE|DROP)( UNIQUE)? INDEX CONCURRENTLY|REINDEX( VERBOSE)? (SCHEMA|DATABASE|SYSTEM)|VACUUM|DISCARD ALL)`)
	pgAlterTypeAddRe  = regexp.MustCompile(`(?i)^ALTER TYPE( .*)? ADD VALUE`)
	mysqlStoredRe     = regexp.MustCompile(`(?i)^CREATE\s(((DEFINER\s(\w+\s)?@\s(\w+\s)?)?(PROCEDURE|FUNCTION|EVENT))|TRIGGER)`)
)

// ParseSQLScript parses a SQL script using PostgreSQL or MySQL Flyway parser
// semantics. dbVersion is PostgreSQL's server_version_num when available; 0
// means unknown and is treated as pre-12 for ALTER TYPE ... ADD VALUE.
func ParseSQLScript(content, driver, path string, dbVersion int) ([]ParsedStatement, error) {
	driver = strings.ToLower(driver)
	if driver == "postgresql" {
		driver = "postgres"
	}
	p := &sqlParser{
		driver:    driver,
		dbVersion: dbVersion,
		path:      path,
		content:   []rune(content),
		line:      1,
		col:       1,
		delimiter: ";",
	}
	var out []ParsedStatement
	for {
		stmt, err := p.nextStatement()
		if err != nil {
			return nil, err
		}
		if stmt == nil {
			break
		}
		out = append(out, *stmt)
	}
	return out, nil
}

func (p *sqlParser) nextStatement() (*ParsedStatement, error) {
	if p.driver == "postgres" {
		p.delimiter = ";"
	}
	p.statementType = ""
	p.parensDepth = 0
	p.blockDepth = 0
	p.blockStack = nil

	statementLine, statementCol := p.line, p.col
	stmtStart := -1
	hasNonComment := false
	var tokens []sqlToken
	var topKeywords []string

	for {
		tok, err := p.nextToken()
		if err != nil {
			return nil, p.wrapParseError(statementLine, statementCol, err)
		}

		if tok.typ == sqlTokenNewDelimiter {
			if hasNonComment {
				sql := p.sliceSQL(stmtStart, tok.start)
				return nil, p.wrapParseError(statementLine, statementCol,
					fmt.Errorf("Delimiter changed inside statement at line %d col %d: %s", statementLine, statementCol, sql))
			}
			p.delimiter = tok.text
			tokens = nil
			stmtStart = -1
			hasNonComment = false
			topKeywords = nil
			statementLine, statementCol = p.line, p.col
			continue
		}

		if tok.typ == sqlTokenDelimiter && !hasNonComment {
			tokens = nil
			stmtStart = -1
			topKeywords = nil
			statementLine, statementCol = p.line, p.col
			continue
		}

		p.adjustBlockDepth(tokens, tok)

		isEnd := tok.typ == sqlTokenEOF ||
			(tok.typ == sqlTokenDelimiter && tok.parensDepth == 0 && p.blockDepth == 0)
		if isEnd {
			if stmtStart < 0 || !hasNonComment {
				return nil, nil
			}
			if tok.typ == sqlTokenEOF && tok.parensDepth > 0 {
				return nil, p.wrapParseError(statementLine, statementCol,
					fmt.Errorf("Incomplete statement at line %d col %d: %s", statementLine, statementCol, p.sliceSQL(stmtStart, tok.start)))
			}
			if tok.typ == sqlTokenEOF && p.blockDepth > 0 {
				return nil, p.wrapParseError(statementLine, statementCol,
					fmt.Errorf("Incomplete statement at line %d col %d: %s", statementLine, statementCol, p.sliceSQL(stmtStart, tok.start)))
			}
			sql := trimJavaSpace(p.sliceSQL(stmtStart, tok.start))
			if sql == "" {
				return nil, nil
			}
			stmt := &ParsedStatement{
				SQL:       sql,
				Line:      statementLine,
				Col:       statementCol,
				Delimiter: p.delimiter,
				Copy:      pgCopyFromStdinRe.MatchString(simplifiedSQL(topKeywords)),
				Batchable: regexp.MustCompile(`^(INSERT|UPDATE|DELETE|UPSERT|MERGE)`).MatchString(simplifiedSQL(topKeywords)),
			}
			stmt.CanExecuteInTransaction = p.canExecuteInTransaction(topKeywords)
			if stmt.Copy && p.driver == "postgres" {
				data, err := p.readCopyData()
				if err != nil {
					return nil, p.wrapParseError(statementLine, statementCol, err)
				}
				stmt.CopyData = data
			}
			return stmt, nil
		}

		if stmtStart < 0 {
			stmtStart = tok.start
			statementLine, statementCol = tok.line, tok.col
		}
		tokens = append(tokens, tok)
		if tok.typ != sqlTokenComment && tok.typ != sqlTokenBlankLines && tok.typ != sqlTokenDelimiter {
			hasNonComment = true
		}
		if tok.typ == sqlTokenKeyword && tok.parensDepth == 0 {
			topKeywords = append(topKeywords, tok.text)
			p.statementType = p.detectStatementType(topKeywords)
		}
	}
}

func (p *sqlParser) detectStatementType(keywords []string) string {
	if p.driver == "mysql" && mysqlStoredRe.MatchString(simplifiedSQL(keywords)) {
		return "stored_program"
	}
	return ""
}

func simplifiedSQL(keywords []string) string {
	return strings.Join(keywords, " ")
}

func (p *sqlParser) canExecuteInTransaction(keywords []string) bool {
	if len(keywords) > 10 {
		return true
	}
	if p.driver != "postgres" {
		return true
	}
	s := simplifiedSQL(keywords)
	if pgNonTxRe.MatchString(s) {
		return false
	}
	if p.dbVersion < 120000 && pgAlterTypeAddRe.MatchString(s) {
		return false
	}
	return true
}

func (p *sqlParser) adjustBlockDepth(tokens []sqlToken, tok sqlToken) {
	if p.driver == "postgres" {
		if tok.typ != sqlTokenKeyword || tok.parensDepth != 0 {
			return
		}
		lex := strings.ToUpper(tok.text)
		prev := previousSQLToken(tokens, tok.parensDepth)
		if lex == "ATOMIC" && prev != nil && strings.EqualFold(prev.text, "BEGIN") {
			p.increaseBlock("ATOMIC")
		}
		if lex == "CASE" && p.currentBlockInitiator() == "ATOMIC" {
			p.increaseBlock("CASE")
		}
		if p.blockDepth > 0 && lex == "END" && (p.currentBlockInitiator() == "ATOMIC" || p.currentBlockInitiator() == "CASE") {
			p.decreaseBlock()
		}
		return
	}

	if p.driver != "mysql" || tok.parensDepth != 0 {
		return
	}
	lex := strings.ToUpper(tok.text)
	prev := previousSQLToken(tokens, tok.parensDepth)
	if tok.typ == sqlTokenKeyword && lex == "BEGIN" && p.statementType == "stored_program" {
		p.increaseBlock("")
	}
	if tok.typ == sqlTokenKeyword && lex == "CASE" && (prev == nil || !strings.EqualFold(prev.text, "END")) {
		p.increaseBlock("")
	}
	if p.blockDepth > 0 && prev != nil && strings.EqualFold(prev.text, "END") &&
		lex != "IF" && lex != "LOOP" && lex != "REPEAT" && lex != "WHILE" {
		p.decreaseBlock()
	}
}

func previousSQLToken(tokens []sqlToken, parens int) *sqlToken {
	for i := len(tokens) - 1; i >= 0; i-- {
		t := tokens[i]
		if t.parensDepth < parens {
			break
		}
		if t.parensDepth != parens || t.typ == sqlTokenComment || t.typ == sqlTokenBlankLines {
			continue
		}
		return &tokens[i]
	}
	return nil
}

func (p *sqlParser) increaseBlock(initiator string) {
	p.blockDepth++
	p.blockStack = append(p.blockStack, initiator)
}
func (p *sqlParser) decreaseBlock() {
	if p.blockDepth > 0 {
		p.blockDepth--
		if len(p.blockStack) > 0 {
			p.blockStack = p.blockStack[:len(p.blockStack)-1]
		}
	}
}
func (p *sqlParser) currentBlockInitiator() string {
	if len(p.blockStack) == 0 {
		return ""
	}
	return p.blockStack[len(p.blockStack)-1]
}

func (p *sqlParser) nextToken() (sqlToken, error) {
	if p.pos >= len(p.content) {
		return sqlToken{typ: sqlTokenEOF, start: p.pos, end: p.pos, line: p.line, col: p.col, parensDepth: p.parensDepth}, nil
	}
	start, line, col := p.pos, p.line, p.col
	rest := string(p.content[p.pos:])

	if p.driver == "postgres" && p.content[p.pos] == '$' {
		if end := p.matchDollarString(); end > p.pos {
			p.consumeTo(end)
			return sqlToken{typ: sqlTokenString, start: start, end: end, line: line, col: col, parensDepth: p.parensDepth}, nil
		}
	}
	c := p.content[p.pos]
	if c == '\'' {
		p.consumeQuoted('\'', false)
		return sqlToken{typ: sqlTokenString, start: start, end: p.pos, line: line, col: col, parensDepth: p.parensDepth}, nil
	}
	if p.driver == "mysql" && c == '"' {
		p.consumeQuoted('"', true)
		return sqlToken{typ: sqlTokenString, start: start, end: p.pos, line: line, col: col, parensDepth: p.parensDepth}, nil
	}
	if p.driver == "postgres" && (strings.HasPrefix(rest, "B'") || strings.HasPrefix(rest, "E'") || strings.HasPrefix(rest, "X'")) {
		p.advance()
		p.consumeQuoted('\'', true)
		return sqlToken{typ: sqlTokenString, start: start, end: p.pos, line: line, col: col, parensDepth: p.parensDepth}, nil
	}
	if p.driver == "postgres" && strings.HasPrefix(rest, "U&'") {
		p.advance()
		p.advance()
		p.consumeQuoted('\'', false)
		return sqlToken{typ: sqlTokenString, start: start, end: p.pos, line: line, col: col, parensDepth: p.parensDepth}, nil
	}
	if c == '(' {
		p.parensDepth++
		p.advance()
		return p.nextToken()
	}
	if c == ')' {
		if p.parensDepth > 0 {
			p.parensDepth--
		}
		p.advance()
		return p.nextToken()
	}
	if p.driver == "mysql" && c == '`' || p.driver == "postgres" && c == '"' {
		q := c
		p.advance()
		var b strings.Builder
		for p.pos < len(p.content) {
			ch := p.content[p.pos]
			if ch == q {
				if p.pos+1 < len(p.content) && p.content[p.pos+1] == q {
					b.WriteRune(q)
					p.advance()
					p.advance()
					continue
				}
				p.advance()
				break
			}
			b.WriteRune(ch)
			p.advance()
		}
		return sqlToken{typ: sqlTokenIdentifier, start: start, end: p.pos, line: line, col: col, text: b.String(), parensDepth: p.parensDepth}, nil
	}
	if strings.HasPrefix(rest, "/*") {
		if p.driver == "mysql" && len(p.content)-p.pos >= 8 && p.content[p.pos+2] == '!' && isASCIIDigit(p.content[p.pos+3]) && isASCIIDigit(p.content[p.pos+4]) && isASCIIDigit(p.content[p.pos+5]) && isASCIIDigit(p.content[p.pos+6]) && isASCIIDigit(p.content[p.pos+7]) {
			p.advance()
			p.advance()
			text := p.readUntilString("*/")
			p.consumeString("*/")
			return sqlToken{typ: sqlTokenCommentDirective, start: start, end: p.pos, line: line, col: col, text: text, parensDepth: p.parensDepth}, nil
		}
		p.readNestedComment()
		return sqlToken{typ: sqlTokenComment, start: start, end: p.pos, line: line, col: col, parensDepth: p.parensDepth}, nil
	}
	if strings.HasPrefix(rest, "--") || (p.driver == "mysql" && c == '#' && !strings.HasPrefix(rest, p.delimiter)) {
		text := p.readUntilChars('\n', '\r')
		return sqlToken{typ: sqlTokenComment, start: start, end: p.pos, line: line, col: col, text: text, parensDepth: p.parensDepth}, nil
	}
	if isSQLDigit(c) {
		for p.pos < len(p.content) && isSQLDigit(p.content[p.pos]) {
			p.advance()
		}
		return sqlToken{typ: sqlTokenNumeric, start: start, end: p.pos, line: line, col: col, text: string(p.content[start:p.pos]), parensDepth: p.parensDepth}, nil
	}
	if p.delimiter != "" && strings.HasPrefix(rest, p.delimiter) {
		p.consumeString(p.delimiter)
		return sqlToken{typ: sqlTokenDelimiter, start: start, end: p.pos, line: line, col: col, text: p.delimiter, parensDepth: p.parensDepth}, nil
	}
	if isSQLLetter(c) {
		wordStart := p.pos
		for p.pos < len(p.content) {
			// A custom delimiter can be adjacent to a keyword (for example
			// END$$). Do not absorb the delimiter into the identifier token.
			if p.delimiter != "" && strings.HasPrefix(string(p.content[p.pos:]), p.delimiter) {
				break
			}
			ch := p.content[p.pos]
			if !isSQLLetter(ch) && !isSQLDigit(ch) && ch != '$' {
				break
			}
			p.advance()
		}
		word := string(p.content[wordStart:p.pos])
		if p.driver == "mysql" && strings.EqualFold(word, "DELIMITER") {
			for p.pos < len(p.content) && p.content[p.pos] != '\n' && p.content[p.pos] != '\r' {
				p.advance()
			}
			text := trimJavaSpace(string(p.content[wordStart:p.pos]))
			parts := strings.Fields(text)
			if len(parts) >= 2 {
				text = parts[1]
			} else {
				text = ""
			}
			if p.pos < len(p.content) {
				p.advance()
			}
			return sqlToken{typ: sqlTokenNewDelimiter, start: start, end: p.pos, line: line, col: col, text: text, parensDepth: p.parensDepth}, nil
		}
		return sqlToken{typ: sqlTokenKeyword, start: start, end: p.pos, line: line, col: col, text: strings.ToUpper(word), parensDepth: p.parensDepth}, nil
	}
	if isJavaSQLSpace(c) {
		var b strings.Builder
		for p.pos < len(p.content) && isJavaSQLSpace(p.content[p.pos]) {
			b.WriteRune(p.content[p.pos])
			p.advance()
		}
		if strings.Count(b.String(), "\n") >= 2 {
			return sqlToken{typ: sqlTokenBlankLines, start: start, end: p.pos, line: line, col: col, text: b.String(), parensDepth: p.parensDepth}, nil
		}
		return p.nextToken()
	}
	p.advance()
	return sqlToken{typ: sqlTokenSymbol, start: start, end: p.pos, line: line, col: col, text: string(c), parensDepth: p.parensDepth}, nil
}

func (p *sqlParser) matchDollarString() int {
	if p.pos >= len(p.content) || p.content[p.pos] != '$' {
		return -1
	}
	j := p.pos + 1
	for j < len(p.content) && (isSQLLetter(p.content[j]) || isSQLDigit(p.content[j]) || p.content[j] == '_') {
		j++
	}
	if j >= len(p.content) || p.content[j] != '$' {
		return -1
	}
	tag := string(p.content[p.pos : j+1])
	endRel := strings.Index(string(p.content[j+1:]), tag)
	if endRel < 0 {
		return -1
	}
	return j + 1 + len([]rune(string(p.content[j+1:])[:endRel])) + len([]rune(tag))
}

func (p *sqlParser) consumeQuoted(q rune, backslash bool) {
	p.advance()
	for p.pos < len(p.content) {
		c := p.content[p.pos]
		if backslash && c == '\\' {
			p.advance()
			if p.pos < len(p.content) {
				p.advance()
			}
			continue
		}
		if c == q {
			if p.pos+1 < len(p.content) && p.content[p.pos+1] == q {
				p.advance()
				p.advance()
				continue
			}
			p.advance()
			break
		}
		p.advance()
	}
}

func (p *sqlParser) readNestedComment() {
	p.consumeString("/*")
	depth := 1
	for p.pos < len(p.content) && depth > 0 {
		rest := string(p.content[p.pos:])
		if strings.HasPrefix(rest, "/*") {
			depth++
			p.consumeString("/*")
		} else if strings.HasPrefix(rest, "*/") {
			depth--
			p.consumeString("*/")
		} else {
			p.advance()
		}
	}
}

func (p *sqlParser) readUntilChars(a, b rune) string {
	start := p.pos
	for p.pos < len(p.content) && p.content[p.pos] != a && p.content[p.pos] != b {
		p.advance()
	}
	return string(p.content[start:p.pos])
}
func (p *sqlParser) readUntilString(s string) string {
	start := p.pos
	for p.pos < len(p.content) && !strings.HasPrefix(string(p.content[p.pos:]), s) {
		p.advance()
	}
	return string(p.content[start:p.pos])
}
func (p *sqlParser) consumeString(s string) {
	for i := 0; i < len([]rune(s)); i++ {
		p.advance()
	}
}
func (p *sqlParser) advance() {
	if p.pos >= len(p.content) {
		return
	}
	c := p.content[p.pos]
	p.pos++
	if c == '\n' {
		p.line++
		p.col = 1
	} else if c == '\r' {
		p.col = 1
	} else {
		p.col++
	}
}
func (p *sqlParser) consumeTo(end int) {
	for p.pos < end {
		p.advance()
	}
}
func (p *sqlParser) sliceSQL(start, end int) string {
	if start < 0 {
		return ""
	}
	if end < start {
		end = start
	}
	return string(p.content[start:end])
}
func (p *sqlParser) readCopyData() (string, error) {
	// Skip the rest of the delimiter line.
	for p.pos < len(p.content) && p.content[p.pos] != '\n' {
		p.advance()
	}
	if p.pos < len(p.content) {
		p.advance()
	}
	var b strings.Builder
	for {
		if p.pos >= len(p.content) {
			return "", fmt.Errorf("Incomplete COPY statement: missing COPY data terminator \\.")
		}
		start := p.pos
		for p.pos < len(p.content) {
			p.advance()
			if p.content[p.pos-1] == '\n' {
				break
			}
		}
		line := string(p.content[start:p.pos])
		if trimJavaSpace(line) == `\.` {
			break
		}
		b.WriteString(line)
	}
	return b.String(), nil
}
func (p *sqlParser) wrapParseError(line, col int, err error) error {
	return fmt.Errorf("Unable to parse statement in %s at line %d col %d. See https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=learn-more-about-flyway/troubleshooting/known-parser-limitations for more information. %s", p.path, line, col, err.Error())
}

func isSQLDigit(c rune) bool   { return c >= '0' && c <= '9' }
func isSQLLetter(c rune) bool  { return c == '_' || unicode.IsLetter(c) }
func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }

// trimJavaSpace mirrors String.trim(): it removes only code points <= 0x20.
func trimJavaSpace(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
}

// isJavaSQLSpace mirrors the whitespace handling in Flyway's parser. Go's
// unicode.IsSpace is intentionally not used because it differs from Java for
// characters such as U+00A0 and U+001C..U+001F.
func isJavaSQLSpace(r rune) bool {
	if r == ' ' || r == '\r' || r == '\u00A0' {
		return true
	}
	return unicode.IsSpace(r) && !unicode.Is(unicode.Zs, r) && r != '\u2007' && r != '\u202F'
}
