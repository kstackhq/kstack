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

package logs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// podSource is a view of the pod name in prod, reading containers, every one with
// none named and all set, previous when asked.
func podSource(name string, previous bool, containers ...string) View {
	return View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: name, Containers: containers, Previous: previous}}}
}

// idsOf is each record's instance.
func idsOf(recs []Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, r.ContainerID)
	}
	return out
}

// An instance read as the current one and, after a restart, as the previous one is
// one row with one copy of each line.
func TestAnInstanceReadAsCurrentThenPreviousHasOneRow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"))
	svc, f := serviceOver(t, webMirror(t), s)
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	restartWeb1(t, f, s, stamped(time.Minute, "a1"))
	page, err := svc.Fetch(t.Context(), "7", podSource("web-1", true, "app"), FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"a0"}, messages(page.Records))
	rows, err := fileOf(t, svc, f).PodStreams(t.Context(), "prod", "web-1", "")
	require.NoError(t, err)
	assert.Len(t, rows, 1, "the file holds instance 0 alone: nothing read instance 1 yet")
	assert.Len(t, chunksOf(t, svc, f, "web-1", "app"), 1)
}

// A restart the mirror reports late is trimmed even when no view reads the
// previous instance: a plain view's resolve learns when the instance before the
// running one finished, and the file's row of it is trimmed before it is read.
func TestALateRestartIsTrimmedUnderAPlainView(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"), stamped(time.Second, "a1"))
	svc, f := serviceOver(t, webMirror(t), s)
	s.mu.Lock()
	s.logs["prod/web-1/app/previous"] = s.logs["prod/web-1/app"]
	s.logs["prod/web-1/app"] = []string{stamped(6*time.Second, "b6"), stamped(7*time.Second, "b7")}
	s.mu.Unlock()
	_, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)

	restarted := pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app", restarted: true}, container{name: "istio-proxy"})
	app := restarted["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
	app["lastState"].(map[string]any)["terminated"].(map[string]any)["finishedAt"] = "2026-10-08T10:00:05Z"
	f.store.Apply(t, restarted)
	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchHead, Limit: 10})

	require.NoError(t, err)
	assert.Equal(t, []string{"b6", "b7"}, messages(page.Records))
	b := containerID("uid-web-1", "app", 1)
	assert.Equal(t, []string{b, b}, idsOf(page.Records))
}

// A page labels a stream as its view reads it, as the follow does: a waiting
// container's log is the instance before, which a view with no Previous reads as the
// container's own, and one with Previous as its previous instance.
func TestAPageLabelsAWaitingContainerAsItsViewReadsIt(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/crash/app", stamped(0, "boom"))
	s.add("prod/crash/app/previous", stamped(0, "boom"))
	f := newMirror(t, append(webObjects(), waiting(pod("crash", "uid-crash", "node-a", nil, container{name: "app", restarted: true})))...)
	svc, _ := serviceOver(t, f, s)

	for _, previous := range []bool{false, true} {
		page, err := svc.Fetch(t.Context(), "7", podSource("crash", previous, "app"), FetchQuery{Mode: FetchHead, Limit: 10})
		require.NoError(t, err)
		require.Equal(t, []string{"boom"}, messages(page.Records))
		assert.Equal(t, previous, page.Records[0].Stream.Previous)
	}
}
