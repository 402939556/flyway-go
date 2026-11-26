package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	nestedResolverPattern = regexp.MustCompile(`(^|[^$])\$\{(([^}]+|)\$\{.+?}).*?\}`)
	resolverPattern       = regexp.MustCompile(`\${1,2}\{[^.]+\.[^.]+?\}`)
	verbatimPattern       = regexp.MustCompile(`^!\{.*\}$`)
)

// PropertyResolverContext resolves Flyway's ${env.KEY} and
// ${placeholder.KEY} configuration values. Environment is intentionally the
// raw process environment because the official EnvironmentVariableResolver
// reads System.getenv directly.
type PropertyResolverContext struct {
	Environment  map[string]string
	Placeholders map[string]string
}

// ResolveConfigurationModelProperties resolves the Flyway model fields and
// plugin root configurations, but not named environments. This mirrors
// ModernConfigurationManager.resolveConfigValues.
func ResolveConfigurationModelProperties(model ConfigurationModel, environment map[string]string) (ConfigurationModel, error) {
	result := model.Clone()
	placeholders := map[string]string{}
	for key, value := range asStringMap(result.Flyway["placeholders"]) {
		placeholders[key] = stringValue(value)
	}
	context := PropertyResolverContext{Environment: environment, Placeholders: placeholders}

	resolvedFlyway, err := context.resolveAny(result.Flyway)
	if err != nil {
		return ConfigurationModel{}, err
	}
	result.Flyway = asStringMap(resolvedFlyway)

	// Flyway 13.13 only resolves the root Flyway model and its plugin
	// configurations. Root configurations and named environments are left
	// untouched here; named environment resolution has separate semantics.
	return result, nil
}

// ResolveValue mirrors PropertyResolverContextImpl.resolveValue.
func (c PropertyResolverContext) ResolveValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if verbatimPattern.MatchString(value) {
		return value[2 : len(value)-1], nil
	}
	if hasNestedResolvers(value) {
		return "", fmt.Errorf("Resolvers cannot be nested: %s", value)
	}
	matches := resolverPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}
	var out strings.Builder
	out.Grow(len(value))
	last := 0
	for _, match := range matches {
		out.WriteString(value[last:match[0]])
		replacement, err := c.resolveMatch(value[match[0]:match[1]])
		if err != nil {
			return "", err
		}
		// Resolved values are inserted directly and never parsed again.
		out.WriteString(replacement)
		last = match[1]
	}
	out.WriteString(value[last:])
	return out.String(), nil
}

func (c PropertyResolverContext) resolveAny(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return c.ResolveValue(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			resolved, err := c.ResolveValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			resolved, err := c.resolveAny(item)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := c.resolveAny(item)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			resolved, err := c.ResolveValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	default:
		return deepCopyValue(value), nil
	}
}

func (c PropertyResolverContext) resolveMatch(match string) (string, error) {
	if strings.HasPrefix(match, "$$") {
		return match[1:], nil
	}
	dot := strings.IndexByte(match, '.')
	if dot < 0 {
		return "", fmt.Errorf("Unknown resolver '%s'", match)
	}
	resolverName := strings.TrimSpace(match[2:dot])
	resolverParam := ""
	filter := ""
	if colon := strings.IndexByte(match, ':'); colon >= 0 {
		resolverParam = strings.TrimSpace(match[dot+1 : colon])
		filter = strings.TrimSpace(match[colon+1 : len(match)-1])
	} else {
		resolverParam = strings.TrimSpace(match[dot+1 : len(match)-1])
	}

	var resolved string
	switch {
	case strings.EqualFold(resolverName, "env"):
		value, ok := c.Environment[resolverParam]
		if !ok {
			return "", fmt.Errorf("Unable to resolve environment variable: '%s'", resolverParam)
		}
		resolved = value
	case strings.EqualFold(resolverName, "placeholder"):
		if resolverParam == "flyway" {
			return "", fmt.Errorf("Only user defined placeholders are supported, but detected attempt to use a default placeholder.")
		}
		value, ok := c.Placeholders[resolverParam]
		if !ok {
			return "", fmt.Errorf("Unable to resolve placeholder: '%s'", resolverParam)
		}
		resolved = value
	default:
		return "", fmt.Errorf("Unknown resolver '%s'", resolverName)
	}
	if filter != "" {
		resolved = filterResolvedValue(resolved, filter)
	}
	return resolved, nil
}

func hasNestedResolvers(value string) bool {
	for _, match := range nestedResolverPattern.FindAllStringSubmatch(value, -1) {
		if len(match) > 2 && resolverPattern.MatchString(match[2]) {
			return true
		}
	}
	return false
}

func filterResolvedValue(value, filter string) string {
	var out strings.Builder
	for _, r := range value {
		if resolverFilterAllows(r, filter) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func resolverFilterAllows(r rune, filter string) bool {
	ascii := r <= unicode.MaxASCII
	return strings.Contains(filter, "D") && unicode.IsDigit(r) ||
		strings.Contains(filter, "A") && unicode.IsLetter(r) ||
		strings.Contains(filter, "a") && unicode.IsLetter(r) && ascii ||
		strings.Contains(filter, "d") && unicode.IsDigit(r) && ascii
}
