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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// A read's failure is fatal for the user's credentials or a claim revoked, denied
// for a 403, ended for any other status under 500 but a 408 or a 429, and
// transient for anything else; a clean end is none.
func TestFailureKind(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]failure{
		nil:                                failNone,
		ErrRevoked:                         failFatal,
		fmt.Errorf("read: %w", ErrRevoked): failFatal,
		&statusError{code: http.StatusUnauthorized}:        failFatal,
		&statusError{code: http.StatusForbidden}:           failDenied,
		&statusError{code: http.StatusBadRequest}:          failEnded,
		&statusError{code: http.StatusNotFound}:            failEnded,
		&statusError{code: http.StatusRequestTimeout}:      failTransient,
		&statusError{code: http.StatusTooManyRequests}:     failTransient,
		&statusError{code: http.StatusInternalServerError}: failTransient,
		errors.New("connection reset"):                     failTransient,
		errRetired:                                         failTransient,
	} {
		assert.Equal(t, want, failureKind(err), "%v", err)
	}
}

// A fatal failure fails the page or the follow with the cluster's words for a
// 401, and as a revoked claim otherwise.
func TestAFatalFailureSaysWhy(t *testing.T) {
	t.Parallel()
	assert.EqualError(t, fatalError(&statusError{code: http.StatusUnauthorized, message: "Unauthorized"}), "the cluster refused to show this log: Unauthorized")
	assert.ErrorIs(t, fatalError(ErrRevoked), ErrRevoked)
}

// A fatal read fails the page.
func TestAFatalReadFailsThePage(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.set("web-1/app", fakeRead{err: &statusError{code: http.StatusUnauthorized, message: "Unauthorized"}})
	backend.set("web-2/app", fakeRead{lines: []Record{{Timestamp: at(0), Message: "a"}}})
	svc, _ := serviceOn(t, backend)

	_, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 10})

	assert.EqualError(t, err, "the cluster refused to show this log: Unauthorized")
}

// A denied or an ended read is the page's skipped entry, and the other streams'
// lines merge as they would without it.
func TestADeniedOrEndedReadIsSkipped(t *testing.T) {
	t.Parallel()
	for _, refused := range []*statusError{{code: http.StatusForbidden, message: "forbidden"}, {code: http.StatusBadRequest, message: "container is waiting to start"}} {
		backend := newFakeBackend()
		backend.set("web-1/app", fakeRead{err: refused})
		backend.set("web-2/app", fakeRead{lines: []Record{{Timestamp: at(0), Message: "a"}, {Timestamp: at(time.Second), Message: "b"}}})
		svc, _ := serviceOn(t, backend)

		page, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 10})

		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, messages(page.Records))
		assert.Equal(t, []string{"web-1/app: " + refused.message}, skippedOf(page))
	}
}

// webPair is a backend whose read of web-1's app is web1 and of web-2's web2.
func webPair(web1, web2 fakeRead) *fakeBackend {
	backend := newFakeBackend()
	backend.set("web-1/app", web1)
	backend.set("web-2/app", web2)
	return backend
}

// awaitOpens waits for backend to open n reads.
func awaitOpens(t *testing.T, backend *fakeBackend, n int) {
	t.Helper()
	for range n {
		backend.opened.Await(t, "a read to open")
	}
}

// A fatal tailer ends the follow, saying why.
func TestAFatalTailerEndsTheFollow(t *testing.T) {
	t.Parallel()
	backend := webPair(fakeRead{err: &statusError{code: http.StatusUnauthorized, message: "Unauthorized"}}, fakeRead{wait: never})
	svc, _ := serviceOn(t, backend)

	stream := openEndingFollow(t, svc)

	assert.Equal(t, Refusal{CodeUnauthorized, "the cluster refused to show this log: Unauthorized"}, endedWith(t, stream))
}

// A transient tailer is opened again once its backoff passes.
func TestATransientTailerIsReopened(t *testing.T) {
	t.Parallel()
	backend := webPair(fakeRead{err: errors.New("connection reset")}, fakeRead{wait: never})
	svc, f := serviceOn(t, backend)
	openFollow(t, svc, f)

	for of := 0; of < 2; {
		if backend.opened.Await(t, "a read to open") == "web-1/app" {
			of++
		}
	}
}

// An ended tailer stays closed until the view sees a later generation of its log,
// then is read again.
func TestAnEndedTailerWaitsForItsGeneration(t *testing.T) {
	t.Parallel()
	backend := webPair(fakeRead{err: &statusError{code: http.StatusBadRequest, message: "container is waiting to start"}}, fakeRead{wait: never})
	svc, f := serviceOn(t, backend)
	stream, _ := openFollow(t, svc, f)
	awaitOpens(t, backend, 2)
	// A negative assertion, so a bounded window: a hundred retries is far past the
	// backoff a dropped stream would wait.
	testutil.NoRecv(t, backend.opened.Chan(), 100*svc.retry, "web-1 to open again")

	backend.set("web-1/app", fakeRead{lines: []Record{{Timestamp: at(time.Hour + time.Second), Message: "started"}}, wait: never})
	f.store.Apply(t, webPod("web-1", "node-a", container{name: "app", restarted: true}, container{name: "istio-proxy"}))
	f.pods <- podFrame(cluster.DeltaFrameModified, "web-1")

	assert.Equal(t, "started", recvLine(t, stream, "web-1's line").Message)
}

// A denied tailer ends its stream alone, and is why the follow ends once every
// stream has.
func TestADeniedTailerEndsItsStreamAlone(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	backend := webPair(fakeRead{err: &statusError{code: http.StatusForbidden, message: "forbidden"}}, fakeRead{err: &statusError{code: http.StatusBadRequest}, wait: done})
	svc, f := serviceOn(t, backend)
	stream, _ := openFollow(t, svc, f)
	awaitOpens(t, backend, 2)
	// A negative assertion, so a bounded window: the follow would end on the 403
	// at once, and a hundred retries is far past it.
	testutil.NoRecv(t, stream.Frames, 100*svc.retry, "the follow to end")

	close(done)

	assert.Equal(t, Refusal{CodeUnauthorized, "the cluster refused to show this log: forbidden"}, endedWith(t, stream))
}
