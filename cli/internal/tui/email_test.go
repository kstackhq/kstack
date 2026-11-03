package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidEmail(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"ada@example.com":         true,
		"ada.lovelace+news@ex.io": true,
		"":                        false,
		"ada":                     false,
		"ada@":                    false,
		"@example.com":            false,
		"ada@example":             false,
		"Ada <ada@example.com>":   false,
		"ada@example.com ":        false,
		"ada @example.com":        false,
	} {
		assert.Equal(t, want, validEmail(in), "%q", in)
	}
}
