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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amorey/gochan/mpsc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// webPod is a pod under web's replica set on node, running containers.
func webPod(name, node string, containers ...container) map[string]any {
	return pod(name, "uid-"+name, node, webReplicaSet(), containers...)
}

// web3 is a third pod of web, on node-a, running app.
func web3() map[string]any { return webPod("web-3", "node-a", container{name: "app"}) }

// web1 and web2 are web's pods as webObjects has them.
func web1() map[string]any {
	return webPod("web-1", "node-a", container{name: "app"}, container{name: "istio-proxy"})
}

func web2() map[string]any {
	return webPod("web-2", "node-b", container{name: "app"}, container{name: "istio-proxy"})
}

// podView reads the default container of the pod name in prod.
func podView(name string) View {
	return View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: name}}}
}

// bookmark closes a watch's snapshot.
var bookmark = cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameBookmark}

// A view of more containers than Kstack reads at once is refused before any is
// read.
func TestAFollowOfTooManyContainersIsRefused(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	addPods(t, f, 99)
	svc, _ := serviceOver(t, f, s)

	_, err := svc.Follow(t.Context(), "7", webView, nil)
	assert.ErrorIs(t, err, ErrTooMany)
	assert.Empty(t, s.requests())
}

// A follow is refused before it opens: a bad cursor, a view the mirror refuses, a
// cluster that cannot be claimed, and a pods watch that cannot be opened, which
// releases the claim.
func TestFollowIsRefusedBeforeItOpens(t *testing.T) {
	bad := "yesterday"
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	_, err := svc.Follow(t.Context(), "7", webView, &bad)
	assert.EqualError(t, err, "a cursor must be an RFC 3339 time, or one a page handed back")

	svc, f := serviceOver(t, webMirror(t), newLogServer())
	f.active.Cache = nil
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.ErrorIs(t, err, ErrNoCache)

	svc, f = serviceOver(t, webMirror(t), newLogServer())
	f.acquireErr = cluster.ErrNotConnectable
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.ErrorIs(t, err, ErrRevoked)

	svc, f = serviceOver(t, webMirror(t), newLogServer())
	f.watchObjectsErr = errors.New("no watch")
	_, err = svc.Follow(t.Context(), "7", webView, nil)
	assert.EqualError(t, err, "no watch")
	assert.Equal(t, int32(1), f.lease.released.Load())
}

// openFollow opens a follow of webView over f, failing the test on an error, and
// closes the pods watch's snapshot, returning once the follow has resolved the
// view against it.
func openFollow(t *testing.T, svc *Service, f *fakeService) (*cluster.Stream[FollowFrame], context.CancelFunc) {
	t.Helper()
	return openFollowOf(t, svc, f, webView)
}

// openFollowOf is openFollow of view.
func openFollowOf(t *testing.T, svc *Service, f *fakeService, view View) (*cluster.Stream[FollowFrame], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	stream, err := svc.Follow(ctx, "7", view, nil)
	require.NoError(t, err)
	closeSnapshot(t, f, f.pods)
	return stream, cancel
}

// openEndingFollow opens a follow of webView over f that ends on its own. It
// leaves the pods watch's snapshot open: the follow opens its streams before the
// snapshot closes and can end before the test could close it, after which nothing
// reads the watch.
func openEndingFollow(t *testing.T, svc *Service) *cluster.Stream[FollowFrame] {
	t.Helper()
	stream, err := svc.Follow(t.Context(), "7", webView, nil)
	require.NoError(t, err)
	return stream
}

// closeSnapshot closes watch's snapshot and returns once the resolve that starts
// has read the mirror, so a change the test makes next cannot race that read.
func closeSnapshot(t *testing.T, f *fakeService, watch chan<- cluster.ClusterCachedDataObjectWatchFrame) {
	t.Helper()
	read := testutil.NewProbe[struct{}](1)
	f.set(func() { f.mirrorRead = read })
	watch <- bookmark
	read.Await(t, "the snapshot's resolve")
	f.set(func() { f.mirrorRead = nil })
}

// A follow on a deployment picks up a pod added to the mirror after it opened,
// from the follow's start, and goes on after one is deleted.
func TestAFollowTracksTheDeploymentsPods(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	assert.Equal(t, "from web-1", recvLine(t, stream, "web-1's line").Message)

	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+2*time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)

	f.store.Delete(t, web1())
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "web-1")
	s.follow("prod/web-2/app") <- stamped(time.Hour+3*time.Second, "from web-2")
	assert.Equal(t, "from web-2", recvLine(t, stream, "web-2's line").Message)

	var sinces []string
	for _, q := range s.requests() {
		if q.Get("sinceTime") != "" {
			sinces = append(sinces, q.Get("sinceTime"))
		}
	}
	assert.Equal(t, []string{"2026-10-08T11:00:00Z", "2026-10-08T11:00:00Z", "2026-10-08T11:00:00Z"}, sinces)
}

// A follow with no cursor reads each log from the moment it started.
func TestAFollowOpensAtItsStart(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "old"))
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour, "new")

	assert.Equal(t, "new", recvLine(t, stream, "the new line").Message)
	assert.Equal(t, "2026-10-08T11:00:00Z", s.requests()[0].Get("sinceTime"))
}

// A follow after a cursor reads every line past it first.
func TestAFollowAfterACursor(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "seen"), stamped(time.Second, "missed"))
	svc, _ := serviceOver(t, webMirror(t), s)

	stream, err := svc.Follow(t.Context(), "7", webView, cursor(0))

	require.NoError(t, err)
	assert.Equal(t, "missed", recvLine(t, stream, "the missed line").Message)
}

// recvLine waits for stream's next frame and answers its line.
func recvLine(t *testing.T, stream *cluster.Stream[FollowFrame], what string) Record {
	t.Helper()
	frame := testutil.Recv(t, stream.Frames, what)
	require.Equal(t, FollowLine, frame.Type, what)
	return *frame.Record
}

// endedWith waits for stream's Ended frame, then for it to end clean, and answers
// the frame's reason.
func endedWith(t *testing.T, stream *cluster.Stream[FollowFrame]) string {
	t.Helper()
	frame := testutil.Recv(t, stream.Frames, "the follow's end")
	require.Equal(t, FollowEnded, frame.Type)
	testutil.RecvClosed(t, stream.Frames, "the follow to end")
	require.NoError(t, stream.Err())
	return frame.Reason
}

// failsWith waits for stream to end with no Ended frame and answers its Err.
func failsWith(t *testing.T, stream *cluster.Stream[FollowFrame]) error {
	t.Helper()
	testutil.RecvClosed(t, stream.Frames, "the follow to end")
	return stream.Err()
}

// awaitClosed waits for key's first follow to end, and awaitReadAgain for a read of
// key from since.
func awaitClosed(t *testing.T, s *logServer, key string) {
	t.Helper()
	require.Eventually(t, func() bool { return s.followsClosed(key) >= 1 },
		testutil.Timeout, time.Millisecond, key+"'s stream to end")
}

func awaitReadAgain(t *testing.T, s *logServer, key, since string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(s.requestsOf(key), func(q url.Values) bool { return q.Get("sinceTime") == since })
	}, testutil.Timeout, time.Millisecond, "a read of "+key+" again from "+since)
}

// A cluster disabled under a follow ends it, saying so.
func TestAFollowEndsWhenTheClusterIsDisabled(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	f.frames = make(chan cluster.ClusterWatchFrame)
	stream, _ := openFollow(t, svc, f)

	disabled := enabledCluster()
	disabled.Spec.Enabled = false
	f.frames <- cluster.ClusterWatchFrame{Type: cluster.DeltaFrameModified, Cluster: disabled}

	assert.Equal(t, ErrRevoked.Reason, endedWith(t, stream))
}

// A 401 on a stream the follow opens ends it: the user's credentials, not a pod's
// state.
func TestAFollowEndsOnAnUnauthorizedStream(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusUnauthorized, "unauthorized"}
	svc, _ := serviceOver(t, webMirror(t), s)
	stream := openEndingFollow(t, svc)

	assert.Equal(t, "the cluster refused to show this log: unauthorized", endedWith(t, stream))
}

// A 403 is one namespace's, so it ends its own stream alone, and the follow ends
// with it only once every stream has: then following again would end the same way.
func TestAForbiddenStreamEndsItselfAlone(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-2/app"] = refusal{http.StatusForbidden, "forbidden"}
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	assert.Equal(t, "from web-1", recvLine(t, stream, "web-1's line").Message)

	s.setRefusal("prod/web-1/app", &refusal{http.StatusForbidden, "forbidden"})
	close(s.follow("prod/web-1/app"))
	assert.Equal(t, "the cluster refused to show this log: forbidden", endedWith(t, stream))
}

// A follow that ends on its own, with its caller still waiting, ends its pods
// watch too, and keeps the reason it ended.
func TestAFollowThatEndsReleasesItsWatch(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusForbidden, "forbidden"}
	s.refuse["prod/web-2/app"] = refusal{http.StatusForbidden, "forbidden"}
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	f.podsDone = make(chan struct{})
	stream := openEndingFollow(t, svc)

	assert.Equal(t, "the cluster refused to show this log: forbidden", endedWith(t, stream))
	testutil.Wait(t, f.podsDone, "the pods watch to end")
}

// A follow whose every container has finished ends, saying so.
func TestAFollowEndsWhenEveryStreamIsGone(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	f.store.Apply(t, finished(web1()))
	f.store.Apply(t, finished(web2()))
	close(s.follow("prod/web-1/app"))
	close(s.follow("prod/web-2/app"))

	assert.Equal(t, errStopped.Reason, endedWith(t, stream))
}

// A follow whose streams all end within the handoff still sends their lines, then
// says every container stopped.
func TestAFollowDeliversWhatItHeldBeforeItEnds(t *testing.T) {
	s := newLogServer()
	s.add("prod/web-1/app", stamped(time.Second, "a"))
	s.add("prod/web-2/app", stamped(0, "b"))
	f := newMirror(t, append(webObjects(), finished(web1()), finished(web2()))...)
	svc, _ := serviceOver(t, f, s)
	svc.handoff = time.Hour
	close(s.follow("prod/web-1/app"))
	close(s.follow("prod/web-2/app"))

	stream, err := svc.Follow(t.Context(), "7", webView, cursor(-time.Second))
	require.NoError(t, err)

	assert.Equal(t, "b", recvLine(t, stream, "web-2's line").Message)
	assert.Equal(t, "a", recvLine(t, stream, "web-1's line").Message)
	assert.Equal(t, errStopped.Reason, endedWith(t, stream))
}

// A pod that joined the view between its resolving and the pods watch opening is
// followed once the watch's snapshot closes, though no frame names it after.
func TestAFollowReconcilesTheSnapshot(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, err := svc.Follow(t.Context(), "7", webView, nil)
	require.NoError(t, err)

	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	f.pods <- bookmark

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow whose last pods leave the view waits for the next, as a Deployment
// scaled to zero and back has it.
func TestAFollowWaitsWhenItsLastPodsLeave(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	f.store.Delete(t, web1())
	f.store.Delete(t, web2())
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "web-1")

	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow of a view with no container yet waits for its pods rather than being
// refused, and reads them from the moment it started.
func TestAFollowOfAnEmptyViewWaitsForItsPods(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	f.store.Mirror(t, "batch/v1", "Job")
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceJob, Name: "batch"}}})

	f.store.Apply(t, workload("batch/v1", "Job", "batch", "uid-batch", nil))
	f.store.Apply(t, pod("batch-1", "uid-batch-1", "", controller("batch/v1", "Job", "batch", "uid-batch"), container{name: "main"}))
	f.pods <- podFrame(cluster.DeltaFrameAdded, "batch-1")
	s.follow("prod/batch-1/main") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "batch-1's line").Message)
	assert.Equal(t, "2026-10-08T11:00:00Z", s.requests()[0].Get("sinceTime"))
}

// A stream that ends clean on a container the view sees running is opened again
// past its last line: a proxy or the API server can close an idle stream.
func TestAFollowReopensAStreamClosedOnARunningContainer(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's line")

	close(s.follow("prod/web-1/app"))

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
}

// A follow narrows its pods watch to the view's namespaces.
func TestAFollowWatchesOnlyItsNamespaces(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1"},
		{Namespace: "prod", Kind: SourcePod, Name: "web-2"},
		{Namespace: "staging", Kind: SourcePod, Name: "web-1"},
	}}
	_, err := svc.Follow(t.Context(), "7", view, nil)
	require.NoError(t, err)

	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, []string{"prod", "staging"}, f.podNamespaces)
}

// A follow of a pod ends when the pod is deleted, and when a pod of the same name
// with another UID stands in its place.
func TestAFollowOfAPodEndsWithThePod(t *testing.T) {
	for name, replacement := range map[string]map[string]any{
		"gone":     nil,
		"replaced": pod("web-1", "uid-other", "node-a", nil, container{name: "app"}),
	} {
		t.Run(name, func(t *testing.T) {
			f := webMirror(t)
			svc, _ := serviceOver(t, f, newLogServer())
			stream, _ := openFollowOf(t, svc, f, podView("web-1"))

			f.store.Delete(t, web1())
			if replacement != nil {
				f.store.Apply(t, replacement)
			}
			f.pods <- uidFrame(cluster.DeltaFrameDeleted, "web-1", "uid-web-1")

			assert.Equal(t, "pod prod/web-1 was deleted", endedWith(t, stream))
		})
	}
}

// A pod deleted under a view that names other sources leaves them followed, and a
// pod of its name with another UID is not read in its place.
func TestADeletedPodLeavesItsViewsOtherSources(t *testing.T) {
	s := newLogServer()
	soloOpens := testutil.NewProbe[string](4)
	s.onOpen = func(key string) {
		if key == "prod/solo/app" {
			soloOpens.Fire(key)
		}
	}
	solo := pod("solo", "uid-a", "node-a", nil, container{name: "app"})
	f := newMirror(t, append(append(webObjects(), webNodes()...), solo)...)
	svc, _ := serviceOver(t, f, s)
	view := webView
	view.Sources = append(slices.Clone(view.Sources), ViewSource{Namespace: "prod", Kind: SourcePod, Name: "solo"})
	stream, _ := openFollowOf(t, svc, f, view)
	soloOpens.Await(t, "solo's first follow")

	f.store.Delete(t, solo)
	f.store.Apply(t, pod("solo", "uid-b", "node-a", nil, container{name: "app"}))
	f.pods <- uidFrame(cluster.DeltaFrameDeleted, "solo", "uid-a")
	// The API server ends a deleted pod's stream.
	close(s.follow("prod/solo/app"))
	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
	// A negative assertion, so a bounded window: a reopen would follow the stream's
	// end after one retry, and a hundred retries is far past a local request's round trip.
	testutil.NoRecv(t, soloOpens.Chan(), 100*svc.retry, "a follow of the pod that took solo's name")
}

// A pod deleted under a view that also reads it through its workload leaves the
// workload reading the pod that takes its name.
func TestADeletedPodLeavesItsWorkloadReadingItsReplacement(t *testing.T) {
	s := newLogServer()
	db := controller("apps/v1", "StatefulSet", "db", "uid-db")
	first := pod("db-0", "uid-db-0", "node-a", db, container{name: "app"})
	f := newMirror(t, append(webNodes(), workload("apps/v1", "StatefulSet", "db", "uid-db", nil), first)...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourceStatefulSet, Name: "db"},
		{Namespace: "prod", Kind: SourcePod, Name: "db-0"},
	}})
	s.follow("prod/db-0/app") <- stamped(time.Hour+time.Second, "from db-0")
	recvLine(t, stream, "db-0's line")

	// On another node, so the follow ends the first pod's stream and opens the
	// replacement's at once.
	f.store.Delete(t, first)
	f.store.Apply(t, pod("db-0", "uid-db-0b", "node-b", db, container{name: "app"}))
	f.pods <- uidFrame(cluster.DeltaFrameDeleted, "db-0", "uid-db-0")

	require.Eventually(t, func() bool { return s.followsOpened("prod/db-0/app") >= 2 },
		testutil.Timeout, time.Millisecond, "the replacement's stream to open")
}

// A follow of a pod ends when the pod is deleted, or a pod of its name with
// another UID stands in its place, with no Deleted frame naming it: before the
// pods watch opened, or while a cache's clear had it closed.
func TestAFollowOfAPodEndsWhenNoFrameNamesItsEnd(t *testing.T) {
	for name, replacement := range map[string]map[string]any{
		"deleted":  nil,
		"replaced": pod("web-1", "uid-other", "node-a", nil, container{name: "app"}),
	} {
		end := func(f *fakeService) {
			f.store.Delete(t, web1())
			if replacement != nil {
				f.store.Apply(t, replacement)
			}
		}
		t.Run(name+" before the watch", func(t *testing.T) {
			f := webMirror(t)
			svc, _ := serviceOver(t, f, newLogServer())
			stream, err := svc.Follow(t.Context(), "7", podView("web-1"), nil)
			require.NoError(t, err)

			end(f)
			f.pods <- bookmark

			assert.Equal(t, "pod prod/web-1 was deleted", endedWith(t, stream))
		})
		t.Run(name+" across a cache clear", func(t *testing.T) {
			f := webMirror(t)
			svc, _ := serviceOver(t, f, newLogServer())
			// No snapshot closes before the clear, so no resolve it starts races the end.
			stream, err := svc.Follow(t.Context(), "7", podView("web-1"), nil)
			require.NoError(t, err)
			pods := make(chan cluster.ClusterCachedDataObjectWatchFrame)
			var ended chan cluster.ClusterCachedDataObjectWatchFrame
			f.set(func() { ended, f.pods = f.pods, pods })

			end(f)
			close(ended)
			// A replacement ends the follow at the resolve after the retry, before the
			// watches open again; a pod missing is only gone once a snapshot says so.
			if replacement == nil {
				pods <- bookmark
			}

			assert.Equal(t, "pod prod/web-1 was deleted", endedWith(t, stream))
		})
	}
}

// A view of several pods ends once every one of them is deleted, naming them.
func TestAFollowOfPodsEndsWhenEveryPodIsDeleted(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1"},
		{Namespace: "prod", Kind: SourcePod, Name: "web-2"},
	}}
	stream, _ := openFollowOf(t, svc, f, view)

	f.store.Delete(t, web1())
	f.store.Delete(t, web2())
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "web-1")
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "web-2")

	assert.Equal(t, "pods prod/web-1, prod/web-2 were deleted", endedWith(t, stream))
}

// A pod the watch deletes and the mirror still holds, as a cache's clear and
// relist send it, is followed on.
func TestAFollowOfAPodOutlastsAResync(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, podView("web-1"))

	f.pods <- uidFrame(cluster.DeltaFrameDeleted, "web-1", "uid-web-1")
	f.pods <- uidFrame(cluster.DeltaFrameAdded, "web-1", "uid-web-1")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "still here")

	assert.Equal(t, "still here", recvLine(t, stream, "web-1's line").Message)
}

// A crash-looping container, waiting to start again, keeps the follow open after
// its stream is refused, and is read once it runs.
func TestAFollowWaitsForAWaitingContainer(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/crash/app"] = refusal{http.StatusBadRequest, "container is waiting to start"}
	f := newMirror(t, append(webObjects(), waiting(pod("crash", "uid-crash", "node-a", nil, container{name: "app", restarted: true})))...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, podView("crash"))
	require.Eventually(t, func() bool { return len(s.requestsOf("prod/crash/app")) >= 1 }, testutil.Timeout, time.Millisecond, "crash asked")
	// A negative assertion, so a bounded window: the follow would end on the refusal
	// at once, and a hundred retries is far past it.
	testutil.NoRecv(t, stream.Frames, 100*svc.retry, "the follow to end")

	s.setRefusal("prod/crash/app", nil)
	f.store.Apply(t, pod("crash", "uid-crash", "node-a", nil, container{name: "app", restarted: true}))
	f.pods <- podFrame(cluster.DeltaFrameModified, "crash")
	s.follow("prod/crash/app") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "crash's line").Message)
}

// An init container that finishes while the main one has yet to start does not
// end the follow of their pod: the main container has no log yet, but will.
func TestAFinishedInitContainerLeavesItsPodFollowed(t *testing.T) {
	s := newLogServer()
	boot := func() map[string]any { return pod("boot", "uid-boot", "node-a", nil, container{name: "app"}) }
	f := newMirror(t, append(webObjects(), withInit(waiting(boot()), "init"))...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "boot", AllContainers: true}}})
	close(s.follow("prod/boot/init"))
	awaitClosed(t, s, "prod/boot/init")
	// A negative assertion, so a bounded window: the follow would end on the init
	// container's end at once, and a hundred retries is far past it.
	testutil.NoRecv(t, stream.Frames, 100*svc.retry, "the follow to end")

	f.store.Apply(t, withInit(boot(), "init"))
	f.pods <- podFrame(cluster.DeltaFrameModified, "boot")
	s.follow("prod/boot/app") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "app's line").Message)
}

// A resolve reads one state of the mirror, whatever commits between its statements:
// a container that starts while the follow asks whether any is starting is still
// waiting to it, never a container that neither runs nor starts, which would end
// the follow.
func TestAResolveReadsOneStateOfTheMirror(t *testing.T) {
	s := newLogServer()
	boot := func() map[string]any { return pod("boot", "uid-boot", "node-a", nil, container{name: "app"}) }
	f := newMirror(t, append(webObjects(), withInit(waiting(boot()), "init"))...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollowOf(t, svc, f, View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "boot", AllContainers: true}}})
	var started sync.Once
	read := testutil.NewProbe[struct{}](1)
	f.set(func() {
		f.mirrorRead = read
		f.beforeStatement = func(text string) {
			if strings.Contains(text, "coalesce(c.container_id, '') = ''") {
				started.Do(func() { f.store.Apply(t, withInit(boot(), "init")) })
			}
		}
	})

	close(s.follow("prod/boot/init"))
	// Once the resolve the init container's end starts has read the mirror, the
	// follow either ends or waits on its watches.
	read.Await(t, "the resolve after init's end")
	select {
	case f.pods <- podFrame(cluster.DeltaFrameModified, "boot"):
	case frame := <-stream.Frames:
		t.Fatalf("the follow sent %s %q while its pod was starting", frame.Type, frame.Reason)
	}
	s.follow("prod/boot/app") <- stamped(time.Hour+time.Second, "started")

	assert.Equal(t, "started", recvLine(t, stream, "app's line").Message)
}

// A mirror answer cut at its bounds ends the follow, since asking again would be
// cut the same way.
func TestAFollowEndsOnACutMirrorAnswer(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	stream, _ := openFollow(t, svc, f)

	f.set(func() { f.moreOn = "FROM objects p" })
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")

	assert.Equal(t, errCut.Reason, endedWith(t, stream))
}

// A mirror watch that ends clean, as a cache's clear ends it, is opened again after
// the retry, and the follow goes on with what its new snapshot shows.
func TestAFollowOutlastsACacheClear(t *testing.T) {
	for _, watch := range []string{"pods", "nodes"} {
		t.Run(watch, func(t *testing.T) {
			s := newLogServer()
			f := webMirror(t)
			svc, _ := serviceOver(t, f, s)
			stream, _ := openFollow(t, svc, f)

			pods := make(chan cluster.ClusterCachedDataObjectWatchFrame)
			var ended chan cluster.ClusterCachedDataObjectWatchFrame
			f.set(func() {
				ended = f.nodes
				if watch == "pods" {
					ended, f.pods = f.pods, pods
				} else {
					f.pods, f.nodes = pods, make(chan cluster.ClusterCachedDataObjectWatchFrame)
				}
			})
			f.store.Apply(t, web3())
			close(ended)
			pods <- bookmark

			s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
			assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
		})
	}
}

// A follow ends when a mirror watch fails, with the watch's reason.
func TestAFollowEndsWithAFailedWatch(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	f.podsErr = errors.New("watch failed")
	stream, _ := openFollow(t, svc, f)
	close(f.pods)
	assert.EqualError(t, failsWith(t, stream), "watch failed")
}

// A follow ends when a workload kind's watch fails, with the watch's reason.
func TestAFollowEndsWithAFailedWorkloadWatch(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	f.workloadsErr = errors.New("watch failed")
	stream, _ := openFollow(t, svc, f)
	close(f.workload("replicasets"))
	assert.EqualError(t, failsWith(t, stream), "watch failed")
}

// A deployment's pod that reaches the mirror before its replica set is followed
// once the replica set arrives, though no pod frame says so.
func TestAFollowWatchesTheWorkloadsBetweenASourceAndItsPods(t *testing.T) {
	s := newLogServer()
	f := newMirror(t, append([]map[string]any{workload("apps/v1", "Deployment", "web", "dep-web", nil), web1()}, webNodes()...)...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	replicaSets := f.workload("replicasets")
	closeSnapshot(t, f, replicaSets)

	f.store.Apply(t, workload("apps/v1", "ReplicaSet", "web-7d9f", "rs-web", controller("apps/v1", "Deployment", "web", "dep-web")))
	replicaSets <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameAdded, Object: &cluster.ClusterCachedDataObject{
		APIVersion: "apps/v1", Kind: "ReplicaSet", Namespace: "prod", Name: "web-7d9f",
	}}

	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	assert.Equal(t, "from web-1", recvLine(t, stream, "web-1's line").Message)
}

// The workload kinds a view watches are each source's own and the kind it
// controls, once each, and none for a pod.
func TestAViewWatchesTheKindsItsSourcesWalk(t *testing.T) {
	view := View{Sources: []ViewSource{
		{Namespace: "prod", Kind: SourcePod, Name: "web-1"},
		{Namespace: "prod", Kind: SourceDeployment, Name: "web"},
		{Namespace: "prod", Kind: SourceReplicaSet, Name: "web-7d9f"},
		{Namespace: "prod", Kind: SourceCronJob, Name: "nightly"},
		{Namespace: "prod", Kind: SourceStatefulSet, Name: "db"},
	}}
	var resources []string
	for _, kind := range view.workloadKinds() {
		resources = append(resources, kind.resource)
	}
	assert.Equal(t, []string{"deployments", "replicasets", "cronjobs", "jobs", "statefulsets"}, resources)
}

// A pod change the mirror cannot be read over leaves the follow as it is, while
// a mirror that fails ends it, and a cluster whose identity moved is one Kstack no
// longer connects to.
func TestAFollowOnAMirrorThatChangesUnderIt(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	stream, _ := openFollowOf(t, svc, f, view)

	// node-b goes first: the snapshot's resolve can land between the deletes, and
	// with node-a gone and node-b kept, web-1 would leave the view as filtered out.
	nodes := webNodes()
	f.store.Delete(t, nodes[1])
	f.store.Delete(t, nodes[0])
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	s.follow("prod/web-1/app") <- stamped(time.Hour, "still here")
	assert.Equal(t, "still here", recvLine(t, stream, "web-1's line").Message)

	f = webMirror(t)
	svc, _ = serviceOver(t, f, newLogServer())
	stream, _ = openFollow(t, svc, f)
	f.set(func() { f.queryErr = errors.New("db closed") })
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	assert.EqualError(t, failsWith(t, stream), "db closed")

	f = webMirror(t)
	svc, _ = serviceOver(t, f, newLogServer())
	stream, _ = openFollow(t, svc, f)
	f.set(func() {
		moved := *f.active.Cache
		moved.Spec.ServerUID = "uid-2"
		f.active.Cache = &moved
	})
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	assert.Equal(t, ErrRevoked.Reason, endedWith(t, stream))
}

// A frame with no object moves nothing: the follow resolves the view again only
// for a pod.
func TestAFrameWithNoObjectIsIgnored(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	f.pods <- cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameModified}
	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A follow cancelled by its context ends clean.
func TestACancelledFollowEndsClean(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	stream, cancel := openFollow(t, svc, f)

	cancel()

	assert.NoError(t, failsWith(t, stream))
}

// A container that restarts is read again past its last line once its old stream
// ends, whether the restart reaches the mirror before that end or after it.
func TestAFollowReopensARestartedContainer(t *testing.T) {
	for name, frameFirst := range map[string]bool{"frame first": true, "end first": false} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			f := webMirror(t)
			svc, _ := serviceOver(t, f, s)
			stream, _ := openFollow(t, svc, f)
			s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
			recvLine(t, stream, "web-1's line")

			restarted := func() {
				f.store.Apply(t, webPod("web-1", "node-a", container{name: "app", restarted: true}, container{name: "istio-proxy"}))
				f.pods <- podFrame(cluster.DeltaFrameModified, "web-1")
			}
			if frameFirst {
				read := testutil.NewProbe[cluster.ClusterCacheHealth](8)
				f.set(func() { f.healthRead = read })
				restarted()
				read.Await(t, "the follow to resolve the view again")
				close(s.follow("prod/web-1/app"))
			} else {
				close(s.follow("prod/web-1/app"))
				awaitClosed(t, s, "prod/web-1/app")
				restarted()
			}

			awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
		})
	}
}

// A container that ends and does not restart is not read again when its pod
// changes for another reason.
func TestAFinishedContainerIsNotReadAgain(t *testing.T) {
	s := newLogServer()
	web1Opens := testutil.NewProbe[string](4)
	s.onOpen = func(key string) {
		if key == "prod/web-1/app" {
			web1Opens.Fire(key)
		}
	}
	f := newMirror(t, append(webObjects(), finished(web1()))...)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	web1Opens.Await(t, "web-1's first follow")
	close(s.follow("prod/web-1/app"))
	awaitClosed(t, s, "prod/web-1/app")

	f.pods <- podFrame(cluster.DeltaFrameModified, "web-1")
	s.follow("prod/web-2/app") <- stamped(time.Hour+time.Second, "from web-2")

	assert.Equal(t, "from web-2", recvLine(t, stream, "web-2's line").Message)
	// A negative assertion, so a bounded window: a reopen would follow the pod frame's
	// resolve at once, and a hundred retries is far past a local request's round trip.
	testutil.NoRecv(t, web1Opens.Chan(), 100*svc.retry, "a follow of web-1 again")
}

// A pod whose node's labels move is read again past its last line, under the new
// labels, not from the follow's start.
func TestAFollowKeepsItsPlaceWhenANodesLabelsMove(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	closeSnapshot(t, f, f.nodes)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's line")

	moved := node("node-a", "eu", "eu-1c", "linux", "amd64")
	f.store.Apply(t, moved)
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, moved)

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
	s.follow("prod/web-1/app") <- stamped(time.Hour+2*time.Second, "after")
	assert.Equal(t, "eu-1c", recvLine(t, stream, "web-1's next line").Place.Zone)
}

// A node that leaves the zone a view filters on ends its pod's stream, and one
// that enters it opens one, with no pod frame between.
func TestAFollowTracksItsNodesLabels(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	view := webView
	view.Filters = []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}
	stream, _ := openFollowOf(t, svc, f, view)
	for _, n := range webNodes() {
		f.nodes <- nodeFrame(cluster.DeltaFrameAdded, n)
	}
	closeSnapshot(t, f, f.nodes)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "from web-1")
	recvLine(t, stream, "web-1's line")

	a, b := node("node-a", "eu", "eu-1c", "linux", "amd64"), node("node-b", "eu", "eu-1a", "linux", "arm64")
	f.store.Apply(t, a)
	f.store.Apply(t, b)
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, a)
	f.nodes <- nodeFrame(cluster.DeltaFrameModified, b)

	awaitClosed(t, s, "prod/web-1/app")
	s.follow("prod/web-2/app") <- stamped(time.Hour+2*time.Second, "from web-2")
	rec := recvLine(t, stream, "web-2's line")
	assert.Equal(t, "from web-2", rec.Message)
	assert.Equal(t, "eu-1a", rec.Place.Zone)
}

// Under a filter on a label, the nodes watch resolves the view again when its
// snapshot closes, and past it for a node that came, went, moved a label a line
// carries, or whose body did not load; a node whose other fields moved resolves
// nothing.
func TestANodeFrameMovesTheViewOnlyForItsLabels(t *testing.T) {
	a := node("node-a", "eu", "eu-1a", "linux", "amd64")
	unloaded := nodeFrame(cluster.DeltaFrameModified, a)
	unloaded.Object.RawJSON = ""
	unlabelled := nodeFrame(cluster.DeltaFrameModified, a)
	unlabelled.Object.UID = "another field"
	for name, tc := range map[string]struct {
		frame cluster.ClusterCachedDataObjectWatchFrame
		moves bool
	}{
		"its snapshot closing": {bookmark, true},
		"the same labels":      {unlabelled, false},
		"a label moved":        {nodeFrame(cluster.DeltaFrameModified, node("node-a", "eu", "eu-1a", "linux", "arm64")), true},
		"a new node":           {nodeFrame(cluster.DeltaFrameAdded, node("node-b", "eu", "eu-1b", "linux", "amd64")), true},
		"a node gone":          {nodeFrame(cluster.DeltaFrameDeleted, a), true},
		"a body not loaded":    {unloaded, true},
		"no object":            {cluster.ClusterCachedDataObjectWatchFrame{Type: cluster.DeltaFrameModified}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &follow{nodes: map[string]Place{}, view: View{Filters: []ViewFilter{{Field: FilterZone, Values: []string{"eu-1a"}}}}}
			done := make(chan cluster.ClusterCachedDataObjectWatchFrame)
			close(done)
			f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, a), done)
			if tc.frame.Type != cluster.DeltaFrameBookmark {
				require.True(t, f.nodesMoved(bookmark, done))
			}

			assert.Equal(t, tc.moves, f.nodesMoved(tc.frame, done))
		})
	}
}

// With no filter on a label, only a node a stream runs on can move the view: a
// frame for any other is not read.
func TestANodeNoStreamRunsOnIsNotRead(t *testing.T) {
	a, b := node("node-a", "eu", "eu-1a", "linux", "amd64"), node("node-b", "eu", "eu-1b", "linux", "arm64")
	f := &follow{nodes: map[string]Place{}, nodesSynced: true, streams: map[StreamKey]*tracked{}}
	key := StreamKey{Pod: "web-1", Container: "app"}
	f.track(key).stream = &opened{st: stream{key: key, place: Place{Node: "node-a"}}}
	done := make(chan cluster.ClusterCachedDataObjectWatchFrame)
	close(done)

	assert.False(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, b), done))
	assert.NotContains(t, f.nodes, "node-b")
	assert.True(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameAdded, a), done))
	b = node("node-b", "eu", "eu-1c", "linux", "arm64")
	assert.False(t, f.nodesMoved(nodeFrame(cluster.DeltaFrameModified, b), done))
}

// A line a replaced stream still delivers is dropped, and moves nothing its
// replacement resumes from: the replacement reads it again.
func TestAFollowDropsALineFromAReplacedStream(t *testing.T) {
	rec := line("web-1", "app", time.Second, "x")
	old, current := &streamRead{key: rec.Stream}, &streamRead{key: rec.Stream}
	f := &follow{streams: map[StreamKey]*tracked{}}
	f.track(rec.Stream).stream = &opened{read: current}

	assert.False(t, f.accept(headed{rec, old}))
	assert.True(t, f.track(rec.Stream).last.isZero())
	assert.True(t, f.accept(headed{rec, current}))
	assert.Equal(t, positionOf(rec), f.track(rec.Stream).last)
}

// A pod that joins while the mirror is syncing is followed once it is ready
// again, with no frame after.
func TestAFollowAsksASyncingMirrorAgain(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)

	read := testutil.NewProbe[cluster.ClusterCacheHealth](8)
	f.set(func() { f.health, f.healthRead = cluster.ClusterCacheHealth{Reason: "Connecting"}, read })
	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	read.Await(t, "the follow to read the syncing cache")
	f.set(func() { f.health = cluster.ClusterCacheHealth{Reason: "Watching"} })

	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")
	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}

// A pod that leaves the view and comes back is read again, past the last line its
// stream sent.
func TestAFollowReopensAPodThatCameBack(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "before")
	recvLine(t, stream, "web-1's first line")

	f.store.Delete(t, web1())
	f.pods <- podFrame(cluster.DeltaFrameDeleted, "web-1")
	awaitClosed(t, s, "prod/web-1/app")
	f.store.Apply(t, web1())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-1")

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:01Z")
}

// A pod that joins a follow opened after a cursor is read from the cursor, like
// the pods it opened with, not from the moment the follow began.
func TestAFollowAfterACursorReadsANewPodFromIt(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	_, err := svc.Follow(t.Context(), "7", webView, cursor(30*time.Minute))
	require.NoError(t, err)
	closeSnapshot(t, f, f.pods)

	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")

	require.Eventually(t, func() bool { return len(s.requests()) == 3 }, testutil.Timeout, time.Millisecond, "web-3's read")
	for _, q := range s.requests() {
		assert.Equal(t, "2026-10-08T10:30:00Z", q.Get("sinceTime"))
	}
}

// A stream the server failed is opened again after the retry, and the follow
// goes on; one the server refused for the log's sake is not.
func TestAFollowReopensADroppedStream(t *testing.T) {
	s := newLogServer()
	s.refuse["prod/web-1/app"] = refusal{http.StatusInternalServerError, "kubelet restarting"}
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	stream, _ := openFollow(t, svc, f)
	require.Eventually(t, func() bool { return len(s.requestsOf("prod/web-1/app")) >= 2 }, testutil.Timeout, time.Millisecond, "web-1 asked again")

	s.setRefusal("prod/web-1/app", nil)
	require.Eventually(t, func() bool { return s.followsOpened("prod/web-1/app") == 1 }, testutil.Timeout, time.Millisecond, "web-1 open")
	s.follow("prod/web-1/app") <- stamped(time.Hour+time.Second, "back")

	assert.Equal(t, "back", recvLine(t, stream, "web-1's line").Message)
}

// A stream whose read dropped is held for its backoff: a reconcile leaves it
// closed, and keeps the follow open while it is the view's only stream, then
// opens it once the hold is over.
func TestAHeldStreamWaitsOutItsBackoff(t *testing.T) {
	s := newLogServer()
	svc, fake := serviceOver(t, webMirror(t), s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, err := acquireClaim(ctx, fake, "7", "uid-1")
	require.NoError(t, err)
	f := &follow{
		s: svc, id: "7", view: View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}}}},
		claim: c, target: target{serverUID: "uid-1"}, fan: newFanIn(), streams: map[StreamKey]*tracked{},
		nodes: map[string]Place{}, deleted: map[string]string{}, podUIDs: map[string]string{}, gone: map[string]bool{},
		dueHub: mpsc.New[StreamKey](0, mpsc.WithKeepOpen()),
	}
	web1 := StreamKey{"prod", "web-1", "app", false}
	f.streams[web1] = &tracked{held: true}

	require.NoError(t, f.reconcile(ctx))
	assert.Nil(t, f.streams[web1].stream)

	f.streams[web1].held = false
	require.NoError(t, f.reconcile(ctx))
	assert.NotNil(t, f.streams[web1].stream)
}

// A dropped stream waits the retry, doubled for each drop in a row up to the cap;
// a stream that ran for the cap, or a line from its successor, starts the count
// again.
func TestADroppingStreamBacksOff(t *testing.T) {
	f := &follow{s: &Service{retry: time.Second, retryCap: time.Minute}, streams: map[StreamKey]*tracked{}}
	tr := &tracked{}

	var waits []time.Duration
	for range 8 {
		waits = append(waits, f.backoff(tr, 0))
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}, waits)
	assert.Equal(t, time.Second, f.backoff(tr, time.Minute))

	r := &streamRead{key: StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}}
	f.streams[r.key] = tr
	tr.stream = &opened{read: r}
	f.accept(headed{Record{Stream: r.key}, r})
	assert.Zero(t, tr.drops)
}

// A stream ends quietly for the log's sake on a status under 500, and is dropped
// on any other failure.
func TestDropped(t *testing.T) {
	assert.False(t, dropped(nil))
	assert.False(t, dropped(&statusError{code: http.StatusBadRequest}))
	assert.True(t, dropped(&statusError{code: http.StatusInternalServerError}))
	assert.True(t, dropped(errors.New("connection reset")))
}

// A follow whose view grows past what Kstack reads at once ends, saying so.
func TestAFollowEndsWhenItsViewGrowsTooLarge(t *testing.T) {
	f := webMirror(t)
	svc, _ := serviceOver(t, f, newLogServer())
	stream, _ := openFollow(t, svc, f)

	addPods(t, f, 99)
	f.pods <- podFrame(cluster.DeltaFrameAdded, "many-0")

	assert.Equal(t, ErrTooMany.Reason, endedWith(t, stream))
}

// Every refusal ends a follow with its words, bare or inside the refusals that
// carry one.
func TestAsRefusalReadsEveryRefusal(t *testing.T) {
	for _, err := range []error{
		ErrSyncing,
		Unmirrored{Refusal: ErrSyncing, Source: 1},
		NoNodes{ErrSyncing},
		fmt.Errorf("wrapped: %w", ErrSyncing),
	} {
		refusal, ok := asRefusal(err)
		assert.True(t, ok, err)
		assert.Equal(t, ErrSyncing, refusal)
	}
	_, ok := asRefusal(errors.New("db closed"))
	assert.False(t, ok)
}

// A pod frame resolves the view at once with no settle; with one, the first frame
// arms one wait that the frames after it share, and a resolve clears it.
func TestAPodFrameSettles(t *testing.T) {
	f := &follow{s: &Service{}}
	assert.True(t, f.settle())

	f.s.settle = time.Hour
	assert.False(t, f.settle())
	armed := f.settled
	require.NotNil(t, armed)
	assert.False(t, f.settle())
	assert.Equal(t, armed, f.settled)
}

// A pod frame under a settle is followed once the settle has passed.
func TestAFollowResolvesOnceThePodFramesSettle(t *testing.T) {
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	svc.settle = time.Millisecond
	stream, _ := openFollow(t, svc, f)

	f.store.Apply(t, web3())
	f.pods <- podFrame(cluster.DeltaFrameAdded, "web-3")
	s.follow("prod/web-3/app") <- stamped(time.Hour+time.Second, "from web-3")

	assert.Equal(t, "from web-3", recvLine(t, stream, "web-3's line").Message)
}
