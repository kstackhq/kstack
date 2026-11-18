// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package githubproxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// graphQLBody is a request's body holding query, and operationName when it is
// not empty.
func graphQLBody(t *testing.T, query, operationName string) []byte {
	t.Helper()
	body := map[string]any{"query": query, "variables": map[string]any{"n": 1}}
	if operationName != "" {
		body["operationName"] = operationName
	}
	out, err := json.Marshal(body)
	require.NoError(t, err)
	return out
}

func TestAQueryIsARead(t *testing.T) {
	t.Parallel()
	for _, q := range []string{
		`{ viewer { login } }`,
		`query Q($n: Int) { viewer { repositories(first: $n) { nodes { name } } } }`,
		`query A { viewer { login } } query B { rateLimit { remaining } }`,
		`query { ...F } fragment F on Query { viewer { login } }`,
	} {
		g, ok := readGraphQL(graphQLBody(t, q, ""))
		require.True(t, ok, q)
		assert.Empty(t, g.mutations, q)
		assert.Zero(t, g.class(), q)
	}
}

// Any mutation in the document makes the request a change, whichever
// operation operationName selects, so the verdict does not rest on reading
// the selection as GitHub does.
func TestAnyMutationIsAChange(t *testing.T) {
	t.Parallel()
	doc := `query Read { viewer { login } } mutation Write { createIssue(input: {}) { issue { id } } addComment(input: {}) { clientMutationId } }`
	for _, op := range []string{"", "Read", "Write", "Missing"} {
		g, ok := readGraphQL(graphQLBody(t, doc, op))
		require.True(t, ok, op)
		assert.Equal(t, []string{"createIssue", "addComment"}, g.mutations, op)
		assert.Equal(t, op, g.operation)
		assert.Equal(t, permissions.UpstreamWrite, g.class(), op)
	}
}

// A mutation's fields are read through its fragments and aliases, so
// deleteRef cannot hide behind either.
func TestAMutationIsReadThroughFragmentsAndAliases(t *testing.T) {
	t.Parallel()
	for _, q := range []string{
		`mutation { deleteRef(input: {refId: "x"}) { clientMutationId } }`,
		`mutation { gone: deleteRef(input: {refId: "x"}) { clientMutationId } }`,
		`mutation { ...D } fragment D on Mutation { deleteRef(input: {refId: "x"}) { clientMutationId } }`,
		`mutation { ... on Mutation { deleteRef(input: {refId: "x"}) { clientMutationId } } }`,
		`mutation { ...D ...D } fragment D on Mutation { ...E } fragment E on Mutation { deleteRef(input: {refId: "x"}) { clientMutationId } }`,
	} {
		g, ok := readGraphQL(graphQLBody(t, q, ""))
		require.True(t, ok, q)
		assert.Contains(t, g.mutations, "deleteRef", q)
		assert.Equal(t, permissions.Destructive, g.class(), q)
	}
}

func TestAGraphQLBodyThatDoesNotReadIsRefused(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"empty":                          ``,
		"not an object":                  `[{"query":"{ viewer { login } }"}]`,
		"a duplicate key":                `{"query":"{ viewer { login } }","query":"mutation { deleteRef(input:{}) { clientMutationId } }"}`,
		"no query":                       `{"variables":{}}`,
		"a non-string query":             `{"query":{"text":"x"}}`,
		"a non-string operation":         `{"query":"{ viewer { login } }","operationName":1}`,
		"trailing data":                  `{"query":"{ viewer { login } }"} {}`,
		"cut short":                      `{"query":"{ viewer { login } }"`,
		"a subscription":                 `{"query":"subscription { x }"}`,
		"a document that does not parse": `{"query":"{ viewer { "}`,
		"no operation":                   `{"query":"fragment F on Query { viewer { login } }"}`,
		"a key that is not a string":     `{1:2}`,
		"a value that does not parse":    `{"query":"{ viewer { login } }","extensions":}`,
		"a missing fragment inside":      `{"query":"mutation { ... on Mutation { ...Gone } }"}`,
		"a missing fragment":             `{"query":"mutation { ...Gone }"}`,
	} {
		_, ok := readGraphQL([]byte(body))
		assert.False(t, ok, name)
	}
	_, ok := readGraphQL([]byte(`{"query":"{ viewer { login } }","operationName":null,"variables":null,"extensions":{}}`))
	assert.True(t, ok, "a null operation name, and keys the proxy does not read")
}
