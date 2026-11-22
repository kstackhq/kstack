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

package memory

import (
	"testing"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countMap(counts []Count) map[string]int {
	out := map[string]int{}
	for _, c := range counts {
		out[scopeKey(c.ClusterID)] = c.Count
	}
	return out
}

func TestCountsWatchEveryScopeAndChanges(t *testing.T) {
	h := newHarness(t)
	h.save(t, clusterB, "b")
	st, err := h.svc.WatchCounts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"b": 1}, countMap(testutil.Recv(t, st.Frames, "initial counts")))

	h.global(t, "shared")
	assert.Equal(t, map[string]int{"": 1, "b": 1}, countMap(testutil.Recv(t, st.Frames, "global count")))
	h.save(t, clusterA, "a")
	assert.Equal(t, map[string]int{"": 1, "a": 1, "b": 1}, countMap(testutil.Recv(t, st.Frames, "cluster count")))

	m := h.get(t, clusterB, "b")
	_, err = h.svc.Update(t.Context(), m.ID, input(nil, "b"))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"": 2, "a": 1}, countMap(testutil.Recv(t, st.Frames, "moved count")))

	require.NoError(t, h.svc.Delete(t.Context(), m.ID))
	assert.Equal(t, map[string]int{"": 1, "a": 1}, countMap(testutil.Recv(t, st.Frames, "deleted count")))

	exec(t, h.db, `DELETE FROM clusters WHERE id = ?`, string(clusterA))
	h.db.Notify(appdb.KeyClusters)
	assert.Equal(t, map[string]int{"": 1}, countMap(testutil.Recv(t, st.Frames, "cascade count")))
	counts, err := h.svc.Counts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"": 1}, countMap(counts))

	require.NoError(t, h.svc.stop(t.Context()))
	testutil.WaitClosed(t, st.Frames, "counts watch")
	_, err = h.svc.WatchCounts(t.Context())
	require.ErrorIs(t, err, ErrStopping)
}

func TestCountsReadFailureEndsWatch(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.WatchCounts(t.Context())
	require.NoError(t, err)
	testutil.Recv(t, st.Frames, "initial counts")
	exec(t, h.db, `DROP TABLE memories`)
	_, err = h.svc.Counts(t.Context())
	require.ErrorContains(t, err, "select memory counts")
	h.db.Notify(appdb.KeyMemories)
	testutil.WaitClosed(t, st.Frames, "failed counts watch")
	require.ErrorContains(t, st.Err(), "select memory counts")
}

func TestCountsWatchStopsWhenNobodyReads(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.WatchCounts(t.Context())
	require.NoError(t, err)
	testutil.Recv(t, st.Frames, "initial counts")
	h.save(t, clusterA, "a")
	testutil.Recv(t, st.Frames, "first change")
	h.save(t, clusterB, "b")
	// The buffered frame is not read; a later change may block a sender.
	h.global(t, "shared")
	require.NoError(t, h.svc.stop(t.Context()))
	for range st.Frames {
	}
	require.NoError(t, st.Err())
}
