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

// Package jsonsettings keeps a settings struct in one JSON file a user can
// edit by hand, refusing each value it cannot read on its own.
package jsonsettings

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

	"github.com/kstackhq/kstack/sidecar/internal/lib/atomicjson"
)

// schemaVersion is the file's layout, stamped under versionKey on every
// write. A change to a field's layout bumps it and upgrades an older file in
// Open.
const (
	schemaVersion = 1
	versionKey    = "schemaVersion"
)

// ErrHeld is an Update that changes a held field without naming it: the write
// would drop the value the store could not read.
var ErrHeld = errors.New("a setting the file holds and Kstack cannot read would be lost")

// A Refusal is one value the store or a check left out. Field is the value's
// key, dotted inside a group (permissions.rules); Value is what the user's
// settings name it by, else its JSON; Reason is in the user's words. As an
// error, it is an Update a check refused.
type Refusal struct {
	Field  string
	Value  string
	Reason string
}

func (r Refusal) Error() string {
	return r.Field + ": " + r.Reason
}

// Store keeps a T in one JSON file and publishes each write. Safe for
// concurrent use.
//
// Each exported field of T is read and written under its JSON name. A field
// of struct type is a group: an object in the file whose fields are read the
// same way, each under its dotted key, so a value refused inside a group
// leaves the group's other fields alone.
type Store[T any] struct {
	file   string
	fields []field
	checks []func(*T) []Refusal
	save   func(file string, v object) error // atomicjson.Save; a test counts the writes

	// refused is fixed at open.
	refused []Refusal
	// held is each restricting field's raw JSON from the file while the field
	// answers its strictest state, so a write keeps it until one writes the
	// field itself.
	held map[string]json.RawMessage
	// unknown is the file less every field and the version: what no field
	// names, kept as read so a write by an older Kstack keeps what a newer
	// one wrote.
	unknown object
	// version is what a write stamps: the file's own when a newer Kstack
	// wrote it, since this build cannot upgrade what it does not know.
	version int

	hub *watch.Hub[T]
	tx  *watch.Sender[T]

	mu  sync.Mutex
	cur T
}

// Open reads file. A missing file is the zero T; one that is not a JSON
// object, or holds a group that is not one, is an error naming the file. A
// value that does not decode, or that a check refuses, is logged and listed by
// Refused, and left out. strictest is how each field that restricts, by its
// key, is set to its most restrictive state: a refused value of one makes the
// store hold the file's raw JSON for it and answer that state instead.
func Open[T any](file string, checks []func(*T) []Refusal, strictest map[string]func(*T)) (*Store[T], error) {
	keys, err := readFile(file)
	if err != nil {
		return nil, err
	}
	t := reflect.TypeFor[T]()
	if err := checkGroups(keys, t, ""); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	fields := fieldsOf(t, nil, nil)
	cur, refused := decode[T](keys, fields)
	refused = append(refused, runChecks(&cur, checks)...)
	held := map[string]json.RawMessage{}
	for _, r := range refused {
		slog.Warn("setting refused", "file", file, "field", r.Field, "value", r.Value, "reason", r.Reason)
		if set, ok := strictest[r.Field]; ok {
			held[r.Field] = keys.get(strings.Split(r.Field, "."))
			set(&cur)
		}
	}
	hub := watch.New(clone(cur))
	return &Store[T]{
		file:    file,
		fields:  fields,
		checks:  checks,
		save:    atomicjson.Save[object],
		refused: refused,
		held:    held,
		unknown: unknownOf(keys, fields),
		version: max(schemaVersion, versionOf(keys)),
		hub:     hub,
		tx:      hub.Sender(),
		cur:     cur,
	}, nil
}

// readFile reads file as one JSON object; a missing file is nil.
func readFile(file string) (object, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys object
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if keys == nil {
		return nil, fmt.Errorf("read %s: not a JSON object", file)
	}
	return keys, nil
}

// checkGroups refuses a group the file holds as anything but an object: there
// is no one field to refuse it as, and reading it as empty would answer the
// zero value of every field in it, restricting ones included.
func checkGroups(keys object, t reflect.Type, prefix string) error {
	for i := range t.NumField() {
		f := t.Field(i)
		name := keyOf(f)
		if name == "" || f.Type.Kind() != reflect.Struct {
			continue
		}
		raw, ok := keys[name]
		if !ok {
			continue
		}
		sub, ok := objectOf(raw)
		if !ok {
			return fmt.Errorf("%s%s is not a JSON object", prefix, name)
		}
		if err := checkGroups(sub, f.Type, prefix+name+"."); err != nil {
			return err
		}
	}
	return nil
}

// versionOf is the version the file was stamped with, 0 when it has none.
func versionOf(keys object) int {
	var v int
	_ = json.Unmarshal(keys[versionKey], &v)
	return v
}

// unknownOf is keys less every field and the version.
func unknownOf(keys object, fields []field) object {
	unknown := object{}
	maps.Copy(unknown, keys)
	for _, f := range fields {
		unknown.delete(f.path)
	}
	delete(unknown, versionKey)
	return unknown
}

func runChecks[T any](v *T, checks []func(*T) []Refusal) []Refusal {
	var refused []Refusal
	for _, check := range checks {
		refused = append(refused, check(v)...)
	}
	return refused
}

// A field is one value of T the file holds under its own key.
type field struct {
	key   string   // dotted, as Refusal.Field and Held name it
	path  []string // the key's names, one per group
	index []int    // for reflect.Value.FieldByIndex
}

// fieldsOf is every field of t, a group's fields in its place, each under
// path and index. A field's name is the one encoding/json writes it under, so
// a save reads back; a field encoding/json skips has none.
func fieldsOf(t reflect.Type, path []string, index []int) []field {
	var fields []field
	for i := range t.NumField() {
		f := t.Field(i)
		name := keyOf(f)
		if name == "" {
			continue
		}
		p := append(slices.Clone(path), name)
		x := append(slices.Clone(index), i)
		if f.Type.Kind() == reflect.Struct {
			fields = append(fields, fieldsOf(f.Type, p, x)...)
			continue
		}
		fields = append(fields, field{key: strings.Join(p, "."), path: p, index: x})
	}
	return fields
}

// keyOf is the JSON name f is read and written under, "" for a field
// encoding/json skips.
func keyOf(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	switch {
	case !f.IsExported() || name == "-":
		return ""
	case name == "":
		return f.Name
	}
	return name
}

// decode sets each field of T from the key that is its JSON name exactly. A
// value that does not decode into its field is refused, and the other fields
// still load; a key no field names is ignored. A null is refused too, since
// encoding/json reads it as the zero value, which for a field that restricts
// is not its strictest state.
func decode[T any](keys object, fields []field) (T, []Refusal) {
	var v T
	var refused []Refusal
	rv := reflect.ValueOf(&v).Elem()
	for _, f := range fields {
		raw := keys.get(f.path)
		if raw == nil {
			continue
		}
		if string(bytes.TrimSpace(raw)) == "null" {
			refused = append(refused, wrongType(f.key, raw))
			continue
		}
		refused = append(refused, decodeField(f.key, raw, rv.FieldByIndex(f.index))...)
	}
	return v, refused
}

// decodeField decodes a list element by element, so one bad element is refused
// alone, and any other value whole. An element with a key its type does not
// name is refused too.
func decodeField(key string, raw json.RawMessage, dst reflect.Value) []Refusal {
	var elems []json.RawMessage
	if dst.Kind() == reflect.Slice && json.Unmarshal(raw, &elems) == nil && elems != nil {
		var refused []Refusal
		list := reflect.MakeSlice(dst.Type(), 0, len(elems))
		for _, elem := range elems {
			v := reflect.New(dst.Type().Elem())
			if err := json.Unmarshal(elem, v.Interface()); err != nil {
				refused = append(refused, wrongType(key, elem))
				continue
			}
			if err := unmarshalStrict(elem, v.Interface()); err != nil {
				refused = append(refused, Refusal{Field: key, Value: string(elem), Reason: "has a key Kstack does not know"})
				continue
			}
			list = reflect.Append(list, v.Elem())
		}
		dst.Set(list)
		return refused
	}
	if err := json.Unmarshal(raw, dst.Addr().Interface()); err != nil {
		dst.SetZero()
		return []Refusal{wrongType(key, raw)}
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

func wrongType(key string, raw json.RawMessage) Refusal {
	return Refusal{Field: key, Value: string(raw), Reason: "is not the type this setting holds"}
}

// Refused is what Open left out of the file, each value with its reason, for
// the store's life.
func (s *Store[T]) Refused() []Refusal {
	return slices.Clone(s.refused)
}

// Get is a copy of the current settings.
func (s *Store[T]) Get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cur)
}

// Update applies fn to a copy under the lock and saves it before returning;
// an error from fn, or a Refusal from the read-back, saves nothing, and a
// result that leaves the file's JSON as it is writes and publishes nothing. fn
// must not call the store: it runs under the lock.
//
// fields names, by key, each field fn sets. A field whose refused raw JSON
// the store keeps is written as fn left it when fields names it, which ends the
// hold: the strictest state a held field already answers can be the user's
// fix. A change to a held field fields does not name is ErrHeld, so no writer
// drops the held value by accident.
func (s *Store[T]) Update(fn func(*T) error, fields ...string) error {
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
func (s *Store[T]) stillHeld(next T, fields []string) (map[string]json.RawMessage, error) {
	was, is := s.valuesOf(s.cur), s.valuesOf(next)
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
func (s *Store[T]) Held(field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.held[field]
	return ok
}

// fileOf is what the file holds for v: what no field names, each field's
// JSON with the held raw values in place of the fields they belong to, and
// the version.
func (s *Store[T]) fileOf(v T, held map[string]json.RawMessage) object {
	file := maps.Clone(s.unknown)
	values := s.valuesOf(v)
	maps.Copy(values, held)
	for _, f := range s.fields {
		if raw, ok := values[f.key]; ok {
			file.set(f.path, raw)
		}
	}
	file[versionKey] = json.RawMessage(strconv.Itoa(s.version))
	return file
}

// valuesOf is each field's JSON, by its key, leaving out what encoding/json
// omits.
func (s *Store[T]) valuesOf(v T) map[string]json.RawMessage {
	b, _ := json.Marshal(v)
	var whole object
	_ = json.Unmarshal(b, &whole)
	values := map[string]json.RawMessage{}
	for _, f := range s.fields {
		if raw := whole.get(f.path); raw != nil {
			values[f.key] = raw
		}
	}
	return values
}

// Subscribe is a current-on-subscribe receiver; close it when done. Receivers
// share each delivery, so a receiver treats it as read-only.
func (s *Store[T]) Subscribe() *watch.Receiver[T] {
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
		panic(fmt.Sprintf("jsonsettings: settings must be JSON-safe: %v", err))
	}
	return out
}

// object is one JSON object of the file, each value as read. A group is an
// object inside it, reached by the path of names down to it.
type object map[string]json.RawMessage

// objectOf is raw as an object, false for any other JSON value.
func objectOf(raw json.RawMessage) (object, bool) {
	var o object
	if json.Unmarshal(raw, &o) != nil || o == nil {
		return nil, false
	}
	return o, true
}

// get is the value at path, nil when it or a group on the way is missing.
func (o object) get(path []string) json.RawMessage {
	raw, ok := o[path[0]]
	if !ok || len(path) == 1 {
		return raw
	}
	sub, ok := objectOf(raw)
	if !ok {
		return nil
	}
	return sub.get(path[1:])
}

// set puts raw at path, adding each group on the way that is missing.
func (o object) set(path []string, raw json.RawMessage) {
	if len(path) == 1 {
		o[path[0]] = raw
		return
	}
	sub, ok := objectOf(o[path[0]])
	if !ok {
		sub = object{}
	}
	sub.set(path[1:], raw)
	o[path[0]], _ = json.Marshal(sub)
}

// delete removes the value at path, and a group it leaves empty.
func (o object) delete(path []string) {
	if len(path) == 1 {
		delete(o, path[0])
		return
	}
	sub, ok := objectOf(o[path[0]])
	if !ok {
		return
	}
	sub.delete(path[1:])
	if len(sub) == 0 {
		delete(o, path[0])
		return
	}
	o[path[0]], _ = json.Marshal(sub)
}
