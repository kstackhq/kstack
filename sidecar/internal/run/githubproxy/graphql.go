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
	"bytes"
	"encoding/json"
	"io"
	"slices"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// destructiveMutations are the mutations that are class 5.
var destructiveMutations = []string{"deleteRef"}

// graphQL is what the proxy reads of a GraphQL request.
type graphQL struct {
	mutations []string // every mutation's top-level fields, in order
	operation string   // operationName, as sent
}

// class is the request's class: a read with no mutation, else a change.
func (g graphQL) class() permissions.Class {
	switch {
	case len(g.mutations) == 0:
		return 0
	case slices.ContainsFunc(g.mutations, func(f string) bool { return slices.Contains(destructiveMutations, f) }):
		return permissions.Destructive
	}
	return permissions.UpstreamWrite
}

// readGraphQL reads body as GitHub would, or refuses it: one JSON object, no
// key twice, a string query, an operationName that is a string or null, and a
// document that parses and holds queries and mutations alone. Any mutation in
// the document makes the request a change, whichever operation it selects.
func readGraphQL(body []byte) (graphQL, bool) {
	query, operation, ok := readBody(body)
	if !ok {
		return graphQL{}, false
	}
	g := graphQL{operation: operation}
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil || len(doc.Operations) == 0 {
		return graphQL{}, false
	}
	for _, op := range doc.Operations {
		switch op.Operation {
		case ast.Query:
		case ast.Mutation:
			fields, ok := fieldsOf(op.SelectionSet, doc.Fragments, map[string]bool{})
			if !ok {
				return graphQL{}, false
			}
			g.mutations = append(g.mutations, fields...)
		default:
			return graphQL{}, false
		}
	}
	return g, true
}

// readBody is body's query and operationName.
func readBody(body []byte) (query, operation string, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", "", false
	}
	var q *string
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", "", false
		}
		key := tok.(string) // an object's key is a string, or Token fails
		if seen[key] {
			return "", "", false
		}
		seen[key] = true
		switch key {
		case "query":
			var text string
			if dec.Decode(&text) != nil {
				return "", "", false
			}
			q = &text
		case "operationName":
			var name *string
			if dec.Decode(&name) != nil {
				return "", "", false
			}
			if name != nil {
				operation = *name
			}
		default:
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return "", "", false
			}
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return "", "", false
	}
	if _, err := dec.Token(); err != io.EOF || q == nil {
		return "", "", false
	}
	return *q, operation, true
}

// fieldsOf is set's top-level field names, read through its inline fragments
// and the fragments it spreads, each once; false for a spread of a fragment
// the document does not hold.
func fieldsOf(set ast.SelectionSet, fragments ast.FragmentDefinitionList, spread map[string]bool) ([]string, bool) {
	var fields []string
	for _, sel := range set {
		var inner ast.SelectionSet
		switch s := sel.(type) {
		case *ast.Field:
			fields = append(fields, s.Name)
			continue
		case *ast.InlineFragment:
			inner = s.SelectionSet
		case *ast.FragmentSpread:
			def := fragments.ForName(s.Name)
			if def == nil {
				return nil, false
			}
			if spread[s.Name] {
				continue
			}
			spread[s.Name] = true
			inner = def.SelectionSet
		}
		more, ok := fieldsOf(inner, fragments, spread)
		if !ok {
			return nil, false
		}
		fields = append(fields, more...)
	}
	return fields, true
}
