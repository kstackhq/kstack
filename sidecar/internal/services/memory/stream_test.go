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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

func (h *harness) watch(t *testing.T, cluster apimeta.ClusterID) *Stream[MemoryWatchFrame] {
	t.Helper()
	st, err := h.svc.Watch(t.Context(), cluster)
	require.NoError(t, err)
	return st
}

// next is the watch's next frame, as its type and the memory's name.
func next(t *testing.T, st *Stream[MemoryWatchFrame]) (apimeta.DeltaFrameType, string) {
	t.Helper()
	f := testutil.Recv(t, st.Frames, "a memory frame")
	if f.Memory == nil {
		return f.Type, ""
	}
	return f.Type, f.Memory.Name
}

func expect(t *testing.T, st *Stream[MemoryWatchFrame], typ apimeta.DeltaFrameType, name string) {
	t.Helper()
	gotType, gotName := next(t, st)
	assert.Equal(t, typ, gotType)
	assert.Equal(t, name, gotName)
}

// A second cluster's changes never arrive: each is followed by one of this
// cluster's, which must be the next frame.
func TestMemoriesWatch(t *testing.T) {
	h := newHarness(t)
	h.save(t, clusterA, "first")
	h.global(t, "shared")

	st := h.watch(t, clusterA)
	expect(t, st, apimeta.DeltaFrameAdded, "first")
	expect(t, st, apimeta.DeltaFrameAdded, "shared")
	expect(t, st, apimeta.DeltaFrameBookmark, "")

	h.save(t, clusterB, "elsewhere")
	h.save(t, clusterA, "second")
	expect(t, st, apimeta.DeltaFrameAdded, "second")

	require.NoError(t, h.svc.Save(t.Context(), clusterA, "second", "rewritten", ""))
	expect(t, st, apimeta.DeltaFrameModified, "second")

	require.NoError(t, h.svc.Forget(t.Context(), clusterA, "first"))
	expect(t, st, apimeta.DeltaFrameDeleted, "first")

	_, err := h.svc.Update(t.Context(), h.get(t, clusterB, "elsewhere").ID, input(nil, "elsewhere"))
	require.NoError(t, err)
	expect(t, st, apimeta.DeltaFrameAdded, "elsewhere")
}

func TestAClustersDeleteLeavesItsWatchAsDeleted(t *testing.T) {
	h := newHarness(t)
	h.save(t, clusterA, "n")
	st := h.watch(t, clusterA)
	expect(t, st, apimeta.DeltaFrameAdded, "n")
	expect(t, st, apimeta.DeltaFrameBookmark, "")

	exec(t, h.db, `DELETE FROM clusters WHERE id = ?`, string(clusterA))
	h.db.Notify(appdb.KeyClusters)
	expect(t, st, apimeta.DeltaFrameDeleted, "n")
}

// The chat and the stamp are off the wire, so a change to either alone sends
// nothing: the next frame is the save that follows.
func TestAChangeOffTheWireSendsNoFrame(t *testing.T) {
	h := newHarness(t)
	exec(t, h.db, `INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', ?, 'chat', 0, 0)`, string(clusterA))
	require.NoError(t, h.svc.Save(t.Context(), clusterA, "n", "b", "c"))
	st := h.watch(t, clusterA)
	expect(t, st, apimeta.DeltaFrameAdded, "n")
	expect(t, st, apimeta.DeltaFrameBookmark, "")

	exec(t, h.db, `DELETE FROM chats WHERE id = 'c'`)
	h.db.Notify(appdb.KeyChats)
	exec(t, h.db, `UPDATE memories SET server_uid = 'uid-9'`)
	h.db.Notify(appdb.KeyMemories)
	h.save(t, clusterA, "next")
	expect(t, st, apimeta.DeltaFrameAdded, "next")
}

func TestAWatchEndsWithTheService(t *testing.T) {
	h := newHarness(t)
	st := h.watch(t, clusterA)
	expect(t, st, apimeta.DeltaFrameBookmark, "")

	require.NoError(t, h.svc.stop(t.Context()))
	testutil.WaitClosed(t, st.Frames, "the frames")
	require.NoError(t, st.Err())

	_, err := h.svc.Watch(t.Context(), clusterA)
	require.ErrorIs(t, err, ErrStopping)
}
