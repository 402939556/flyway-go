package core

import (
	"strings"
	"testing"
)

func TestPropertyResolverEnvPlaceholderVerbatimAndEscapes(t *testing.T) {
	ctx := PropertyResolverContext{
		Environment: map[string]string{
			"DB_USER":     "alice",
			"DB_PASSWORD": "p=ss",
			"UNICODE":     "A1é٣",
			"DOLLAR":      "$1",
		},
		Placeholders: map[string]string{"schema": "app"},
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "environment", in: "user=${env.DB_USER}", want: "user=alice"},
		{name: "placeholder", in: "${placeholder.schema}", want: "app"},
		{name: "escaped resolver", in: "$${env.DB_USER}", want: "${env.DB_USER}"},
		{name: "verbatim", in: "!{keep ${env.DB_USER} exactly}", want: "keep ${env.DB_USER} exactly"},
		{name: "replacement not reparsed", in: "${env.DB_USER}-${env.DB_PASSWORD}", want: "alice-p=ss"},
		{name: "replacement dollar not reparsed", in: "${env.DOLLAR}", want: "$1"},
		{name: "trim surrounding", in: "  ${env.DB_USER}  ", want: "alice"},
		{name: "unicode digits filter", in: "${env.UNICODE:D}", want: "1٣"},
		{name: "unicode letters filter", in: "${env.UNICODE:A}", want: "Aé"},
		{name: "ascii letters filter", in: "${env.UNICODE:a}", want: "A"},
		{name: "ascii digits filter", in: "${env.UNICODE:d}", want: "1"},
		{name: "colon form is not a resolver", in: "${env:DB_USER}", want: "${env:DB_USER}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ctx.ResolveValue(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("ResolveValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPropertyResolverErrors(t *testing.T) {
	ctx := PropertyResolverContext{Environment: map[string]string{}, Placeholders: map[string]string{"x": "y"}}
	cases := []struct {
		in   string
		want string
	}{
		{in: "${env.MISSING}", want: "Unable to resolve environment variable: 'MISSING'"},
		{in: "${placeholder.missing}", want: "Unable to resolve placeholder: 'missing'"},
		{in: "${placeholder.flyway}", want: "Only user defined placeholders are supported"},
		{in: "${bogus.x}", want: "Unknown resolver 'bogus'"},
		{in: "${env.${placeholder.x}}", want: "Resolvers cannot be nested"},
	}
	for _, tc := range cases {
		_, err := ctx.ResolveValue(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ResolveValue(%q) error = %v, want contains %q", tc.in, err, tc.want)
		}
	}
}

func TestResolveConfigurationModelPropertiesRootOnly(t *testing.T) {
	model := ConfigurationModel{
		RootConfigurations: map[string]any{
			"plugin": "${env.SHOULD_NOT_RESOLVE_ROOT}",
		},
		Flyway: map[string]any{
			"locations": []any{"filesystem:${env.MIGRATIONS}"},
			"placeholders": map[string]any{
				"schema": "${env.SCHEMA}",
			},
		},
		Environments: map[string]map[string]any{
			"prod": {
				"url": "${env.SHOULD_NOT_RESOLVE}",
				"flyway": map[string]any{
					"table": "${env.SHOULD_NOT_RESOLVE}",
				},
			},
		},
	}
	resolved, err := ResolveConfigurationModelProperties(model, map[string]string{"MIGRATIONS": "migrations", "SCHEMA": "app"})
	if err != nil {
		t.Fatal(err)
	}
	locations := stringListValue(resolved.Flyway["locations"])
	if len(locations) != 1 || locations[0] != "filesystem:migrations" {
		t.Fatalf("locations = %#v", locations)
	}
	if got := asStringMap(resolved.Flyway["placeholders"])["schema"]; got != "app" {
		t.Fatalf("placeholder schema = %#v", got)
	}
	if got := resolved.Environments["prod"]["url"]; got != "${env.SHOULD_NOT_RESOLVE}" {
		t.Fatalf("environment was unexpectedly resolved: %#v", got)
	}
	if got := resolved.RootConfigurations["plugin"]; got != "${env.SHOULD_NOT_RESOLVE_ROOT}" {
		t.Fatalf("root configuration was unexpectedly resolved: %#v", got)
	}
}
