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

// Package sandboxconfig keeps the sandbox's settings in <data>/sandbox.json.
package sandboxconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/atomicjson"
)

// Settings is the sandbox's settings. Each field is added by the step that
// needs it; see the table in the spec.
type Settings struct{}

// Store keeps Settings in one JSON file and publishes each write. Safe for
// concurrent use.
type Store = store[Settings]

// An Option changes how Open reads the file.
type Option func(*options)

type options struct {
	checks []func(*Settings) []Refusal
}

// WithChecks runs checks in place of the read-back's own. It is a test seam:
// production passes none.
func WithChecks(checks ...func(*Settings) []Refusal) Option {
	return func(o *options) { o.checks = checks }
}

// Open reads file. A missing file is empty Settings; one that is not a JSON
// object is an error naming the file, since a sandbox with no settings is one
// the user cannot see. A value the read-back refuses is logged and listed by
// Refused, and left out, or for a field that restricts, answered as that
// field's strictest state.
func Open(file string, opts ...Option) (*Store, error) {
	o := options{checks: checks}
	for _, opt := range opts {
		opt(&o)
	}
	return openStore(file, o.checks, strictest)
}

// store is Store over any settings type, so the tests can run it over fields
// Settings does not have yet.
type store[T any] struct {
	file   string
	checks []func(*T) []Refusal
	save   func(file string, v map[string]json.RawMessage) error // atomicjson.Save; a test counts the writes

	// refused is fixed at open.
	refused []Refusal
	// held is each restricting field's raw JSON from the file while the field
	// answers its strictest state, so a write keeps it until one writes the
	// field itself.
	held map[string]json.RawMessage

	hub *watch.Hub[T]
	tx  *watch.Sender[T]

	mu  sync.Mutex
	cur T
}

func openStore[T any](file string, checks []func(*T) []Refusal, strictest map[string]func(*T)) (*store[T], error) {
	keys, err := readKeys(file)
	if err != nil {
		return nil, err
	}
	cur, refused := decode[T](keys)
	refused = append(refused, runChecks(&cur, checks)...)
	held := map[string]json.RawMessage{}
	for _, r := range refused {
		if set, ok := strictest[r.Field]; ok {
			held[r.Field] = keys[r.Field]
			set(&cur)
		}
	}
	for _, r := range refused {
		slog.Warn("sandbox setting refused", "file", file, "field", r.Field, "value", r.Value, "reason", r.Reason)
	}
	hub := watch.New(clone(cur))
	return &store[T]{
		file:    file,
		checks:  checks,
		save:    atomicjson.Save[map[string]json.RawMessage],
		refused: refused,
		held:    held,
		hub:     hub,
		tx:      hub.Sender(),
		cur:     cur,
	}, nil
}

// readKeys reads file as one JSON object; a missing file is nil.
func readKeys(file string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if keys == nil {
		return nil, fmt.Errorf("read %s: not a JSON object", file)
	}
	return keys, nil
}

// decode sets each field of T from the key that is its JSON name exactly. A
// value that does not decode into its field is refused, and the other fields
// still load; a key no field names is ignored.
func decode[T any](keys map[string]json.RawMessage) (T, []Refusal) {
	var v T
	var refused []Refusal
	rv := reflect.ValueOf(&v).Elem()
	for i := range rv.NumField() {
		// The key is the one encoding/json writes the field under, so a save reads back.
		f := rv.Type().Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		raw, ok := keys[name]
		if !ok {
			continue
		}
		refused = append(refused, decodeField(name, raw, rv.Field(i))...)
	}
	return v, refused
}

// decodeField decodes a list element by element, so one bad element is refused
// alone, and any other value whole.
func decodeField(name string, raw json.RawMessage, field reflect.Value) []Refusal {
	var elems []json.RawMessage
	if field.Kind() == reflect.Slice && json.Unmarshal(raw, &elems) == nil && elems != nil {
		var refused []Refusal
		list := reflect.MakeSlice(field.Type(), 0, len(elems))
		for _, elem := range elems {
			v := reflect.New(field.Type().Elem())
			if err := json.Unmarshal(elem, v.Interface()); err != nil {
				refused = append(refused, wrongType(name, elem))
				continue
			}
			list = reflect.Append(list, v.Elem())
		}
		field.Set(list)
		return refused
	}
	if err := json.Unmarshal(raw, field.Addr().Interface()); err != nil {
		field.SetZero()
		return []Refusal{wrongType(name, raw)}
	}
	return nil
}

func wrongType(field string, raw json.RawMessage) Refusal {
	return Refusal{Field: field, Value: string(raw), Reason: "is not the type this setting holds"}
}

// Refused is what Open left out of the file, each value with its reason, for
// the store's life.
func (s *store[T]) Refused() []Refusal {
	return slices.Clone(s.refused)
}

// Get is a copy of the current settings.
func (s *store[T]) Get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cur)
}

// Update applies fn to a copy under the lock and saves it before returning;
// an error from fn, or a Refusal from the read-back, saves nothing, and a
// result that leaves the file's JSON as it is writes and publishes nothing. fn
// must not call the store: it runs under the lock.
//
// fields names, by JSON key, each field fn sets. A field whose refused raw JSON
// the store keeps is written as fn left it when fn changes it or fields names
// it: the strictest state a held field already answers can be the user's fix.
func (s *store[T]) Update(fn func(*T) error, fields ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cur)
	if err := fn(&next); err != nil {
		return err
	}
	if refused := runChecks(&next, s.checks); len(refused) > 0 {
		return refused[0]
	}
	held := s.stillHeld(next, fields)
	was, _ := json.Marshal(fileOf(s.cur, s.held))
	is := fileOf(next, held)
	if b, _ := json.Marshal(is); bytes.Equal(was, b) {
		return nil
	}
	if err := s.save(s.file, is); err != nil {
		return err
	}
	s.cur = clone(next) // fn may still hold what it put in next
	s.held = held
	s.tx.Send(clone(next)) //nolint:errcheck // Send never blocks, and the hub is never closed
	return nil
}

// stillHeld is the held fields an Update leaves alone: changing a field, or
// naming it in fields, replaces the raw JSON kept for it.
func (s *store[T]) stillHeld(next T, fields []string) map[string]json.RawMessage {
	was, is := fileOf(s.cur, nil), fileOf(next, nil)
	held := maps.Clone(s.held)
	maps.DeleteFunc(held, func(key string, _ json.RawMessage) bool {
		return slices.Contains(fields, key) || !bytes.Equal(was[key], is[key])
	})
	return held
}

// fileOf is what the file holds for v: each field's JSON, with the held raw
// values in place of the fields they belong to.
func fileOf[T any](v T, held map[string]json.RawMessage) map[string]json.RawMessage {
	b, _ := json.Marshal(v)
	keys := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &keys)
	maps.Copy(keys, held)
	return keys
}

// Subscribe is a current-on-subscribe receiver; close it when done. Receivers
// share each delivery, so a receiver treats it as read-only.
func (s *store[T]) Subscribe() *watch.Receiver[T] {
	return s.hub.Receiver()
}

// clone copies v through JSON, so a field added later cannot be missed. An
// empty slice or map comes back nil, which omitempty makes the same JSON.
func clone[T any](v T) T {
	var out T
	b, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	if err != nil {
		panic(fmt.Sprintf("sandboxconfig: settings must be JSON-safe: %v", err))
	}
	return out
}
