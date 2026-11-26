package core

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// LoadConfigurationFromString mirrors ConfigUtils.loadConfigurationFromString.
//
// The exact order is important: trim and double every backslash first, decide
// whether a trailing backslash is a continuation, append a newline, expand
// ${ENV} variables unless raw mode is active, and finally run java.util.Properties
// parsing. Flyway deliberately keeps a trailing backslash in Windows paths and
// only restores it when the following line can not be a new flyway property.
func LoadConfigurationFromString(content string, raw bool) (map[string]string, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i, line := range lines {
		line = strings.ReplaceAll(strings.TrimSpace(line), "\\", "\\\\")
		if strings.HasSuffix(line, `\\`) && i < len(lines)-1 {
			nextLine := lines[i+1]
			restoreMultilineDelimiter := false
			if nextLine != "" {
				if strings.HasPrefix(strings.TrimSpace(nextLine), "flyway.") && strings.Contains(nextLine, "=") {
					restoreMultilineDelimiter = isMultilineBooleanExpression(nextLine)
				} else {
					restoreMultilineDelimiter = true
				}
			}
			if restoreMultilineDelimiter {
				line = line[:len(line)-2] + `\`
			}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	contents := b.String()
	if !raw {
		contents = expandEnvironmentVariables(contents)
	}
	props, err := parseJavaProperties(contents)
	if err != nil {
		return nil, err
	}
	return props, nil
}

func isMultilineBooleanExpression(line string) bool {
	return !strings.HasPrefix(line, "shouldExecute") &&
		(strings.Contains(line, "==") || strings.Contains(line, "!="))
}

func loadConfigurationFile(path string, raw bool) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return LoadConfigurationFromString(string(b), raw)
}

func endsWithOddBackslash(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// expandEnvironmentVariables implements ConfigUtils.expandEnvironmentVariables.
// Replacement values are literal: '$' and '\' in the value are not interpreted.
func expandEnvironmentVariables(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+2 < len(s) && s[i] == '$' && s[i+1] == '{' {
			j := i + 2
			for j < len(s) && (isASCIIAlphaNum(s[j]) || s[j] == '_') {
				j++
			}
			if j > i+2 && j < len(s) && s[j] == '}' {
				b.WriteString(os.Getenv(s[i+2 : j]))
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isASCIIAlphaNum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// parseJavaProperties mirrors Properties.load for the subset Flyway needs,
// including escaped separators, comments and physical-line continuations.
func parseJavaProperties(content string) (map[string]string, error) {
	out := make(map[string]string)
	physical := strings.Split(content, "\n")
	for i := 0; i < len(physical); i++ {
		line := physical[i]
		if i == len(physical)-1 && line == "" {
			continue
		}
		j := 0
		for j < len(line) && isPropertyWhitespace(line[j]) {
			j++
		}
		if j == len(line) {
			continue
		}
		if line[j] == '#' || line[j] == '!' {
			continue
		}
		line = line[j:]

		for endsWithOddBackslash(line) && i+1 < len(physical) {
			line = line[:len(line)-1]
			i++
			next := physical[i]
			k := 0
			for k < len(next) && isPropertyWhitespace(next[k]) {
				k++
			}
			line += next[k:]
		}
		key, value, err := parseJavaPropertyLine(line)
		if err != nil {
			return nil, err
		}
		if key != "" {
			out[key] = value
		}
	}
	return out, nil
}

func isPropertyWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\f'
}

func parseJavaPropertyLine(line string) (string, string, error) {
	i := 0
	var key strings.Builder
	for i < len(line) {
		c := line[i]
		if c == '\\' {
			decoded, consumed, err := decodeJavaPropertyEscape(line[i:])
			if err != nil {
				return "", "", err
			}
			key.WriteString(decoded)
			i += consumed
			continue
		}
		if c == '=' || c == ':' || isPropertyWhitespace(c) {
			break
		}
		key.WriteByte(c)
		i++
	}

	for i < len(line) && isPropertyWhitespace(line[i]) {
		i++
	}
	if i < len(line) && (line[i] == '=' || line[i] == ':') {
		i++
	}
	for i < len(line) && isPropertyWhitespace(line[i]) {
		i++
	}

	var value strings.Builder
	for i < len(line) {
		if line[i] == '\\' {
			decoded, consumed, err := decodeJavaPropertyEscape(line[i:])
			if err != nil {
				return "", "", err
			}
			value.WriteString(decoded)
			i += consumed
			continue
		}
		value.WriteByte(line[i])
		i++
	}
	return key.String(), value.String(), nil
}

func decodeJavaPropertyEscape(s string) (string, int, error) {
	if len(s) < 2 || s[0] != '\\' {
		return s[:1], 1, nil
	}
	switch s[1] {
	case 't':
		return "\t", 2, nil
	case 'n':
		return "\n", 2, nil
	case 'r':
		return "\r", 2, nil
	case 'f':
		return "\f", 2, nil
	case 'u':
		if len(s) < 6 {
			return "", 0, fmt.Errorf("Malformed \\uxxxx encoding")
		}
		var r uint16
		for i := 2; i < 6; i++ {
			r <<= 4
			switch c := s[i]; {
			case c >= '0' && c <= '9':
				r += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				r += uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				r += uint16(c-'A') + 10
			default:
				return "", 0, fmt.Errorf("Malformed \\uxxxx encoding")
			}
		}
		return string(utf16.Decode([]uint16{r})), 6, nil
	default:
		// Unknown escapes in java.util.Properties simply lose the backslash.
		return string(s[1]), 2, nil
	}
}

// ParseBoolean is the exact boolean conversion used by ConfigUtils.removeBoolean.
func ParseBoolean(key, value string) (bool, error) {
	switch {
	case strings.EqualFold(value, "true"):
		return true, nil
	case strings.EqualFold(value, "false"):
		return false, nil
	default:
		return false, fmt.Errorf("Invalid value for %s (should be either true or false): %s", key, value)
	}
}

func unknownConfigurationError(keys []string, prefix *string) error {
	if len(keys) == 0 {
		return nil
	}
	noun := "property"
	if len(keys) > 1 {
		noun = "properties"
	}
	location := "script config files"
	if prefix != nil {
		location = "conf files or commandline parameters"
	}
	return fmt.Errorf("Unknown configuration %s: %s. Please check your %s", noun, strings.Join(keys, ", "), location)
}
