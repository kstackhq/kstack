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

package appsettings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
)

func (h *harness) watch(t *testing.T) *Stream {
	t.Helper()
	st, err := h.svc.Watch(t.Context())
	require.NoError(t, err)
	return st
}

// expect is the watch's next frame, which must hold id as the default.
func expect(t *testing.T, st *Stream, id apimeta.ClusterID) {
	t.Helper()
	assert.Equal(t, id, idOf(testutil.Recv(t, st.Frames, "an app settings frame")))
}

func TestTheWatchOpensWithTheRowAndFollowsItsWrites(t *testing.T) {
	h := newHarness(t)
	h.setDefault(t, prod)

	st := h.watch(t)
	expect(t, st, prod)

	h.setDefault(t, dev)
	expect(t, st, dev)
	h.setDefault(t, "")
	expect(t, st, "")
}

func TestAClustersDeleteReachesTheWatchAsNoDefault(t *testing.T) {
	h := newHarness(t)
	h.setDefault(t, prod)
	st := h.watch(t)
	expect(t, st, prod)

	exec(t, h.db, `DELETE FROM clusters WHERE id = ?`, string(prod))
	h.db.Notify(appdb.KeyClusters)
	expect(t, st, "")
}

// A ping that left the row as it was sends nothing: the next frame is the
// write that follows.
func TestAnUnchangedRowSendsNoFrame(t *testing.T) {
	h := newHarness(t)
	h.setDefault(t, prod)
	st := h.watch(t)
	expect(t, st, prod)

	h.db.Notify(appdb.KeyClusters)
	h.setDefault(t, prod)
	h.setDefault(t, dev)
	expect(t, st, dev)
}

func TestAWatchEndsWithTheService(t *testing.T) {
	h := newHarness(t)
	st := h.watch(t)
	expect(t, st, "")

	require.NoError(t, h.svc.stop(t.Context()))
	testutil.WaitClosed(t, st.Frames, "the frames")
	require.NoError(t, st.Err())

	_, err := h.svc.Watch(t.Context())
	require.ErrorIs(t, err, ErrStopping)
}

func TestAWatchEndsWithItsReadsFailure(t *testing.T) {
	h := newHarness(t)
	st := h.watch(t)
	expect(t, st, "")

	exec(t, h.db, `DROP TABLE app_settings`)
	h.db.Notify(appdb.KeyAppSettings)

	testutil.WaitClosed(t, st.Frames, "the frames")
	require.ErrorContains(t, st.Err(), "get app settings")
}
