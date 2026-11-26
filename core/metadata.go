package core

import (
	"fmt"
	"os"
	"strings"
)

const redgateShouldExecuteMessage = "Flyway Redgate Edition Required: shouldExecute is not supported by OSS Edition\nDownload Redgate Edition for free: https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=redgate-edition-download"

// SQLScriptMetadata mirrors Flyway's SqlScriptMetadata.
type SQLScriptMetadata struct {
	ExecuteInTransaction    *bool
	Encoding                string
	PlaceholderReplacement  *bool
	ShouldExecute           bool
	ShouldExecuteExpression string
}

func emptySQLScriptMetadata() SQLScriptMetadata {
	return SQLScriptMetadata{ShouldExecute: true}
}

// LoadSQLScriptMetadata loads <migration>.conf. raw keys are retained so the
// unmapped shouldExecute expression can be reported by Flyway's OSS error path.
func LoadSQLScriptMetadata(path string, placeholders map[string]string, prefix, suffix string) (SQLScriptMetadata, error) {
	return LoadSQLScriptMetadataWithEncoding(path, placeholders, prefix, suffix, ":", "UTF-8")
}

// LoadSQLScriptMetadataWithEncoding reads the .conf resource using the same
// configured charset as FileSystemResource. The metadata's own encoding value
// only applies to the SQL script, never to the .conf file itself.
func LoadSQLScriptMetadataWithEncoding(path string, placeholders map[string]string, prefix, suffix, separator, encoding string) (SQLScriptMetadata, error) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptySQLScriptMetadata(), nil
		}
		return SQLScriptMetadata{}, err
	}
	decoded, err := DecodeScript(rawBytes, encoding, false)
	if err != nil {
		return SQLScriptMetadata{}, fmt.Errorf("Failed to read %s: %w", path, err)
	}
	raw, err := LoadConfigurationFromString(decoded, true)
	if err != nil {
		return SQLScriptMetadata{}, err
	}
	mappedText := decoded
	if len(placeholders) != 0 {
		mappedText, err = ReplacePlaceholders(mappedText, placeholders, prefix, suffix, separator)
		if err != nil {
			return SQLScriptMetadata{}, err
		}
	}
	mapped, err := LoadConfigurationFromString(mappedText, false)
	if err != nil {
		return SQLScriptMetadata{}, err
	}
	md := emptySQLScriptMetadata()
	if v, ok := mapped["executeInTransaction"]; ok {
		b, err := ParseBoolean("executeInTransaction", v)
		if err != nil {
			return SQLScriptMetadata{}, err
		}
		md.ExecuteInTransaction = &b
		delete(mapped, "executeInTransaction")
	}
	if v, ok := mapped["encoding"]; ok {
		md.Encoding = v
		delete(mapped, "encoding")
	}
	if v, ok := mapped["placeholderReplacement"]; ok {
		b, err := ParseBoolean("placeholderReplacement", v)
		if err != nil {
			return SQLScriptMetadata{}, err
		}
		md.PlaceholderReplacement = &b
		delete(mapped, "placeholderReplacement")
	}
	if _, ok := mapped["shouldExecute"]; ok {
		if expr, ok := raw["shouldExecute"]; ok {
			md.ShouldExecuteExpression = expr
		}
		return SQLScriptMetadata{}, fmt.Errorf("%s", redgateShouldExecuteMessage)
	}
	var unknown []string
	for k := range mapped {
		unknown = append(unknown, k)
	}
	if err := unknownConfigurationError(unknown, nil); err != nil {
		return SQLScriptMetadata{}, err
	}
	return md, nil
}

// MigrationMetadataPath is the official resource relative path plus .conf.
func MigrationMetadataPath(scriptPath string) string { return scriptPath + ".conf" }

func effectivePlaceholderReplacement(global bool, override *bool) bool {
	if override != nil {
		return *override
	}
	return global
}

func effectiveExecuteInTransaction(global bool, override *bool, nonTransactional bool) bool {
	if override != nil {
		return *override
	}
	return global && !nonTransactional
}

func validateMixedStatementKinds(statements []ParsedStatement, mixed bool, metadata *bool, global bool) error {
	if mixed || metadata != nil || !global {
		return nil
	}
	txFound, nonTxFound := false, false
	for _, st := range statements {
		if st.CanExecuteInTransaction {
			txFound = true
		} else {
			nonTxFound = true
		}
		if txFound && nonTxFound {
			suffix := ""
			if !st.CanExecuteInTransaction {
				suffix = " [non-transactional]"
			}
			return fmt.Errorf("Detected both transactional and non-transactional statements within the same migration (even though mixed is false). Offending statement found at line %d: %s%s", st.Line, st.SQL, suffix)
		}
	}
	return nil
}

func normalizeEncodingName(name string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
}
