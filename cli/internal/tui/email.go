package tui

import (
	"net/mail"
	"strings"
)

// validEmail reports whether s is a bare address (no display name) whose
// domain has a dot, so a typo like "ada@example" is caught before submitting.
func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	_, domain, _ := strings.Cut(s, "@")
	return strings.Contains(domain, ".")
}
