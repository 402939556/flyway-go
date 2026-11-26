package core

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultSchemaResolution(t *testing.T) {
	tests := []struct {
		name        string
		configured  []string
		defaultName string
		current     string
		wantDefault string
		wantSchemas []string
		wantErr     string
	}{
		{
			name:        "configured only",
			configured:  []string{"app", "audit"},
			wantDefault: "app",
			wantSchemas: []string{"app", "audit"},
		},
		{
			name:        "explicit default prepended",
			configured:  []string{"app", "audit"},
			defaultName: "audit",
			wantDefault: "audit",
			wantSchemas: []string{"app", "audit"},
		},
		{
			name:        "explicit default not configured",
			configured:  []string{"app"},
			defaultName: "history",
			wantDefault: "history",
			wantSchemas: []string{"history", "app"},
		},
		{
			name:        "current schema fallback",
			current:     "public",
			wantDefault: "public",
			wantSchemas: []string{"public"},
		},
		{
			name:    "unable to determine current schema",
			wantErr: "Unable to determine schema for the schema history table. Set a default schema for the connection or specify one using the 'defaultSchema' property",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDefault, gotSchemas, err := resolveSchemaNames(tt.configured, tt.defaultName, tt.current, nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gotDefault != tt.wantDefault {
				t.Fatalf("default = %q, want %q", gotDefault, tt.wantDefault)
			}
			if !reflect.DeepEqual(gotSchemas, tt.wantSchemas) {
				t.Fatalf("schemas = %#v, want %#v", gotSchemas, tt.wantSchemas)
			}
		})
	}
}
