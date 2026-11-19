package graph

import (
	"slices"
	"testing"
)

// The auth projection is the whole distance between the Kstack account and the webview, and it
// is one schema edit wide: AuthState binds onto connectors.State and Identity onto oauth.Identity,
// so a field declared here may be served with no Go code written to review. Pinning both types'
// fields is what makes that edit fail rather than ship.
func TestAuthProjectionCarriesNoTokens(t *testing.T) {
	for _, tc := range []struct {
		typeName string
		want     []string
	}{
		{"AuthState", []string{"authenticated", "identity"}},
		{"Identity", []string{"email", "name", "sub"}},
	} {
		def := parsedSchema.Types[tc.typeName]
		if def == nil {
			t.Fatalf("%s: not in the schema", tc.typeName)
		}
		var got []string
		for _, f := range def.Fields {
			got = append(got, f.Name)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s fields = %v, want %v — a field added here may be served straight off its Go type; if it carries no credential, add it to this list", tc.typeName, got, tc.want)
		}
	}
}
