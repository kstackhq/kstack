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

// Package securityconfig keeps the security settings in <data>/security.json.
package securityconfig

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
	"strconv"
	"strings"
	"sync"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/atomicjson"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// Settings is the security settings. Each field is added by the step that
// needs it.
type Settings struct {
	Path []PathEntry `json:"path,omitempty"` // the user's PATH, frozen, in the shell's order
	// PathResolved is set by the first sync: before it a run searches the
	// platform's default PATH, after it the list alone.
	PathResolved bool `json:"pathResolved,omitempty"`
	// PathStrict is set by a Remove that ends the store's hold on path, and
	// cleared by the next sync, which it makes file every new entry pending:
	// the entry the store could not read may have been a removal.
	PathStrict bool `json:"pathStrict,omitempty"`

	DefaultMode permissions.Mode   `json:"defaultMode,omitempty"` // Ask when empty
	Modes       []ContextMode      `json:"modes,omitempty"`       // first match wins
	Rules       []permissions.Rule `json:"rules,omitempty"`       // the always rules, the user's

	Executables []Executable `json:"executables,omitempty"` // the user's registered tools, in the order added
}

// schemaVersion is the file's layout, stamped under versionKey on every
// write. A step that changes a field's layout bumps it and upgrades an older
// file in Open.
const (
	schemaVersion = 1
	versionKey    = "schemaVersion"
)

// ErrHeld is an Update that changes a held field without naming it: the write
// would drop the value the store could not read.
var ErrHeld = errors.New("a setting the file holds and Kstack cannot read would be lost")

// Store keeps Settings in one JSON file and publishes each write. Safe for
// concurrent use. It wraps the generic store so the permission readers in
// permissions.go can be its methods.
type Store struct {
	*store[Settings]
}

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
	s, err := openStore(file, o.checks, strictest)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
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
	// unknown is each key of the file no field names, kept as read so a write
	// by an older Kstack keeps what a newer one wrote.
	unknown map[string]json.RawMessage
	// version is what a write stamps: the file's own when a newer Kstack
	// wrote it, since this build cannot upgrade what it does not know.
	version int

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
	fields := fieldKeys[T]()
	unknown := map[string]json.RawMessage{}
	for key, raw := range keys {
		if key != versionKey && !slices.Contains(fields, key) {
			unknown[key] = raw
		}
	}
	for _, r := range refused {
		slog.Warn("security setting refused", "file", file, "field", r.Field, "value", r.Value, "reason", r.Reason)
	}
	hub := watch.New(clone(cur))
	return &store[T]{
		file:    file,
		checks:  checks,
		save:    atomicjson.Save[map[string]json.RawMessage],
		refused: refused,
		held:    held,
		unknown: unknown,
		version: max(schemaVersion, versionOf(keys)),
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

// versionOf is the version the file was stamped with, 0 when it has none.
func versionOf(keys map[string]json.RawMessage) int {
	var v int
	_ = json.Unmarshal(keys[versionKey], &v)
	return v
}

// fieldKeys is the key each field of T is read and written under, by the
// field's index; "" for a field encoding/json skips. The key is the one
// encoding/json writes the field under, so a save reads back.
func fieldKeys[T any]() []string {
	t := reflect.TypeFor[T]()
	keys := make([]string, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys[i] = name
	}
	return keys
}

// decode sets each field of T from the key that is its JSON name exactly. A
// value that does not decode into its field is refused, and the other fields
// still load; a key no field names is ignored. A null is refused too, since
// encoding/json reads it as the zero value, which for a field that restricts
// is not its strictest state.
func decode[T any](keys map[string]json.RawMessage) (T, []Refusal) {
	var v T
	var refused []Refusal
	rv := reflect.ValueOf(&v).Elem()
	for i, name := range fieldKeys[T]() {
		raw, ok := keys[name]
		if name == "" || !ok {
			continue
		}
		if string(bytes.TrimSpace(raw)) == "null" {
			refused = append(refused, wrongType(name, raw))
			continue
		}
		refused = append(refused, decodeField(name, raw, rv.Field(i))...)
	}
	return v, refused
}

// decodeField decodes a list element by element, so one bad element is refused
// alone, and any other value whole. An element with a key its type does not
// name is refused too.
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
			if err := unmarshalStrict(elem, v.Interface()); err != nil {
				refused = append(refused, Refusal{Field: name, Value: string(elem), Reason: "has a key Kstack does not know"})
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

// unmarshalStrict refuses a key no field names. An element's fields narrow
// it, so a misspelled one dropped would leave a rule matching more than its
// author wrote.
func unmarshalStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
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
// the store keeps is written as fn left it when fields names it, which ends the
// hold: the strictest state a held field already answers can be the user's
// fix. A change to a held field fields does not name is ErrHeld, so no writer
// drops the held value by accident.
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
	held, err := s.stillHeld(next, fields)
	if err != nil {
		return err
	}
	was, _ := json.Marshal(s.fileOf(s.cur, s.held))
	is := s.fileOf(next, held)
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

// stillHeld is the held fields an Update leaves held: naming a field in
// fields replaces the raw JSON kept for it, and changing one unnamed is
// ErrHeld.
func (s *store[T]) stillHeld(next T, fields []string) (map[string]json.RawMessage, error) {
	was, is := fieldsOf(s.cur), fieldsOf(next)
	held := maps.Clone(s.held)
	for key := range s.held {
		switch {
		case slices.Contains(fields, key):
			delete(held, key)
		case !bytes.Equal(was[key], is[key]):
			return nil, fmt.Errorf("%s: %w", key, ErrHeld)
		}
	}
	return held, nil
}

// Held reports whether the store still keeps field's raw JSON, by its key: the
// file holds a value of it the store refused, and no write has named it since.
func (s *store[T]) Held(field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.held[field]
	return ok
}

// fileOf is what the file holds for v: each field's JSON, with the held raw
// values in place of the fields they belong to, then the keys no field names,
// and the version.
func (s *store[T]) fileOf(v T, held map[string]json.RawMessage) map[string]json.RawMessage {
	keys := fieldsOf(v)
	maps.Copy(keys, held)
	maps.Copy(keys, s.unknown)
	keys[versionKey] = json.RawMessage(strconv.Itoa(s.version))
	return keys
}

// fieldsOf is each field's JSON, by its key.
func fieldsOf[T any](v T) map[string]json.RawMessage {
	b, _ := json.Marshal(v)
	keys := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &keys)
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
		panic(fmt.Sprintf("securityconfig: settings must be JSON-safe: %v", err))
	}
	return out
}
