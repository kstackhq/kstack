//go:build tools

// Pins gqlgen in go.sum so `gqlgen generate` works on a fresh checkout. The
// protoc plugins are `tool` dependencies in go.mod.
package tools

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/graphql/introspection"
)
