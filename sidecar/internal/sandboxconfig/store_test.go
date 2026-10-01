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

package sandboxconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/amorey/gochan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// testSettings stands in for Settings, which has no fields until a later step
// adds them.
type testSettings struct {
	Names  []string `json:"names,omitempty"`
	Count  int      `json:"count,omitempty"`
	Denied []string `json:"denied,omitempty"`
}

// Denied restricts, and denying everything is its most restrictive state.
var testStrictest = map[string]func(*testSettings){
	"denied": func(v *testSettings) { v.Denied = []string{"*"} },
}

func openTest(t *testing.T, file string, checks ...func(*testSettings) []Refusal) *store[testSettings] {
	t.Helper()
	s, err := openStore(file, checks, testStrictest)
	require.NoError(t, err)
	return s
}

func TestTheStorePersists(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	s := openTest(t, file)
	assert.Equal(t, testSettings{}, s.Get(), "a missing file opens empty")
	assert.NoFileExists(t, file, "Open writes nothing")

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Names = []string{"a", "b"}
		v.Count = 2
		return nil
	}))
	assert.Equal(t, testSettings{Names: []string{"a", "b"}, Count: 2}, openTest(t, file).Get())

	_, err := Open(filepath.Join(t.TempDir(), "sandbox.json"))
	require.NoError(t, err, "the production store opens a missing file")
}

func TestABadFileFailsOpen(t *testing.T) {
	for _, body := range []string{"{", "[]", "null"} {
		t.Run(body, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "sandbox.json")
			require.NoError(t, os.WriteFile(file, []byte(body), 0o600))

			_, err := Open(file)
			require.ErrorContains(t, err, file)
		})
	}
}

// A file that cannot be read fails Open too, rather than opening empty.
func TestAnUnreadableFileFailsOpen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.Mkdir(file, 0o700))

	_, err := Open(file)
	require.ErrorContains(t, err, file)
}

func TestAFieldOfTheWrongTypeIsRefusedAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	body := `{"names": "a", "count": 3, "other": true, "Count": 9}`
	require.NoError(t, os.WriteFile(file, []byte(body), 0o600))

	s := openTest(t, file)
	assert.Equal(t, testSettings{Count: 3}, s.Get(), "the other field loads, and a key in another case is unknown")
	refused := s.Refused()
	require.Len(t, refused, 1, "an unknown key is ignored")
	assert.Equal(t, "names", refused[0].Field)
	assert.Equal(t, `"a"`, refused[0].Value)
	assert.NotEmpty(t, refused[0].Reason)
}

func TestAnEqualUpdateWritesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	s := openTest(t, file)
	rx := s.Subscribe()
	defer rx.Close()
	_, err := rx.TryRecv()
	require.NoError(t, err)

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Names = []string{}
		return nil
	}))
	assert.NoFileExists(t, file, "an empty slice is the same JSON as a nil one")
	_, err = rx.TryRecv()
	assert.ErrorIs(t, err, gochan.ErrEmpty, "nothing is published")
}

func TestUpdateIsOneWrite(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "sandbox.json"))
	var saves atomic.Int32
	var fail atomic.Bool
	save := s.save
	s.save = func(file string, v map[string]json.RawMessage) error {
		if fail.Load() {
			return errBoom
		}
		saves.Add(1)
		return save(file, v)
	}
	increment := func(v *testSettings) error {
		v.Count++
		return nil
	}

	require.NoError(t, s.Update(increment))
	assert.Equal(t, int32(1), saves.Load(), "one Update is one save")

	require.ErrorIs(t, s.Update(func(v *testSettings) error {
		v.Count = 100
		return errBoom
	}), errBoom)
	require.NoError(t, s.Update(func(*testSettings) error { return nil }))
	assert.Equal(t, int32(1), saves.Load(), "neither an error nor no change saves")
	assert.Equal(t, 1, s.Get().Count)

	fail.Store(true)
	require.ErrorIs(t, s.Update(increment), errBoom)
	assert.Equal(t, 1, s.Get().Count, "a failed save keeps the value")
	fail.Store(false)

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { assert.NoError(t, s.Update(increment)) })
	}
	wg.Wait()
	assert.Equal(t, 3, s.Get().Count, "the later Update sees the earlier's result")
	assert.Equal(t, int32(3), saves.Load())
}

var errBoom = errors.New("boom")

func TestSubscribeSeesTheLatestWrite(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "sandbox.json"))
	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Names = []string{"a"}
		return nil
	}))

	rx := s.Subscribe()
	defer rx.Close()
	got, err := rx.TryRecv()
	require.NoError(t, err, "the current value on subscribe")
	assert.Equal(t, []string{"a"}, got.Names)

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Names = append(v.Names, "b")
		return nil
	}))
	got, err = rx.TryRecv()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got.Names)

	got.Names[0] = "changed"
	mine := s.Get()
	mine.Names[1] = "changed"
	assert.Equal(t, []string{"a", "b"}, s.Get().Names, "neither copy reaches the store's value")
}

func TestARefusedValueIsLeftOutAndListed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"names": ["a"], "count": 1}`), 0o600))
	logs := testutil.CaptureLogs(t)

	ran := false
	refuseOnce := func(v *testSettings) []Refusal {
		if ran || v.Names == nil {
			return nil
		}
		ran = true
		v.Names = nil
		return []Refusal{{Field: "names", Value: "a", Reason: "is refused"}}
	}
	atMostNine := func(v *testSettings) []Refusal {
		if v.Count <= 9 {
			return nil
		}
		return []Refusal{{Field: "count", Value: strconv.Itoa(v.Count), Reason: "is more than nine"}}
	}
	s := openTest(t, file, refuseOnce, atMostNine)

	want := []Refusal{{Field: "names", Value: "a", Reason: "is refused"}}
	assert.Equal(t, testSettings{Count: 1}, s.Get(), "the refused value is left out")
	assert.Equal(t, want, s.Refused())
	assert.Contains(t, logs.String(), "is refused")

	before, err := os.ReadFile(file)
	require.NoError(t, err)
	err = s.Update(func(v *testSettings) error {
		v.Count = 10
		return nil
	})
	var refusal Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "is more than nine", refusal.Reason)
	assert.EqualError(t, err, "count: is more than nine")
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused Update writes nothing")
	assert.Equal(t, 1, s.Get().Count)

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Count = 2
		return nil
	}))
	after, err = os.ReadFile(file)
	require.NoError(t, err)
	assert.JSONEq(t, `{"count": 2}`, string(after), "the next write drops the refused value")
	assert.Equal(t, want, s.Refused(), "and it is still listed")
}

// A field decodes from the key a save writes it under: its Go name when it has
// no JSON name, and never for a field encoding/json skips, unexported included.
func TestDecodeReadsTheKeyASaveWrites(t *testing.T) {
	type fields struct {
		Plain  int
		Hidden int `json:"-"`
		inner  int
	}
	v, refused := decode[fields](map[string]json.RawMessage{
		"Plain": json.RawMessage("1"), "-": json.RawMessage("2"), "inner": json.RawMessage("3"),
	})
	assert.Empty(t, refused)
	assert.Equal(t, fields{Plain: 1}, v)
}

// The store keeps its own copy of what Update wrote, so data the callback
// still holds cannot change it afterwards.
func TestUpdateKeepsItsOwnCopy(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "sandbox.json"))
	mine := []string{"a"}
	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Names = mine
		return nil
	}))

	mine[0] = "changed"
	assert.Equal(t, []string{"a"}, s.Get().Names)
}

func TestOneBadElementIsRefusedAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"names": ["a", 1, "b"]}`), 0o600))

	s := openTest(t, file)
	assert.Equal(t, []string{"a", "b"}, s.Get().Names)
	assert.Equal(t, []Refusal{{Field: "names", Value: "1", Reason: "is not the type this setting holds"}}, s.Refused())
}

func TestARefusedRestrictionFailsClosed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"denied": ["a", 1]}`), 0o600))
	assert.Equal(t, []string{"*"}, openTest(t, file).Get().Denied, "a bad element")

	require.NoError(t, os.WriteFile(file, []byte(`{"denied": "a"}`), 0o600))
	assert.Equal(t, []string{"*"}, openTest(t, file).Get().Denied, "a value of the wrong type")

	require.NoError(t, os.WriteFile(file, []byte(`{"denied": ["a", "b"]}`), 0o600))
	refuseB := func(v *testSettings) []Refusal {
		if !slices.Contains(v.Denied, "b") {
			return nil
		}
		v.Denied = slices.DeleteFunc(v.Denied, func(d string) bool { return d == "b" })
		return []Refusal{{Field: "denied", Value: "b", Reason: "is refused"}}
	}
	assert.Equal(t, []string{"*"}, openTest(t, file, refuseB).Get().Denied, "a value a check refuses")
}

func TestAnUpdateKeepsARefusedRestrictionInTheFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"denied": ["a", 1]}`), 0o600))
	s := openTest(t, file)

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Count = 1
		return nil
	}))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.JSONEq(t, `{"count": 1, "denied": ["a", 1]}`, string(data), "an unrelated write keeps the raw value")
	assert.Equal(t, []string{"*"}, s.Get().Denied)

	require.NoError(t, s.Update(func(v *testSettings) error {
		v.Denied = []string{"a"}
		return nil
	}))
	data, err = os.ReadFile(file)
	require.NoError(t, err)
	assert.JSONEq(t, `{"count": 1, "denied": ["a"]}`, string(data), "a write of the field itself replaces it")
}

// The fix can be the strictest state the field already answers, which changes
// nothing in memory; naming the field is what writes it.
func TestAnUpdateNamingARefusedRestrictionWritesItsStrictestState(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"denied": ["a", 1]}`), 0o600))
	s := openTest(t, file)
	rx := s.Subscribe()
	defer rx.Close()
	_, err := rx.TryRecv()
	require.NoError(t, err)

	setStrictest := func(v *testSettings) error {
		v.Denied = []string{"*"}
		return nil
	}
	require.NoError(t, s.Update(setStrictest))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.JSONEq(t, `{"denied": ["a", 1]}`, string(data), "an Update that does not name the field keeps the raw value")

	require.NoError(t, s.Update(setStrictest, "denied"))
	data, err = os.ReadFile(file)
	require.NoError(t, err)
	assert.JSONEq(t, `{"denied": ["*"]}`, string(data), "an Update naming the field writes it")
	_, err = rx.TryRecv()
	assert.NoError(t, err, "the write is published")
}

func TestCloneRefusesAValueThatIsNotJSON(t *testing.T) {
	assert.Panics(t, func() { clone(struct{ C chan int }{}) })
}
