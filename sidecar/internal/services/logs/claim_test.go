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
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// endless writes a line each time lines sends, until the request ends.
func endless(lines <-chan string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case line := <-lines:
				_, _ = w.Write([]byte(line + "\n"))
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
}

// A claim reaches the API server the mirror's pods came from and no other.
func TestAClaimIsRefusedAnotherServer(t *testing.T) {
	svc := serving(t, endless(nil))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-2")
	require.NoError(t, err)
	defer c.close()

	_, err = c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)

	assert.ErrorIs(t, err, cluster.ErrIdentityMismatch)
}

// A cluster Kstack cannot connect to is refused before anything is read, and any
// other failure to claim it is the failure.
func TestAClaimOnACLusterKstackDoesNotConnectTo(t *testing.T) {
	_, err := acquireClaim(t.Context(), &fakeService{acquireErr: cluster.ErrNotConnectable}, "7", "uid-1")
	assert.ErrorIs(t, err, ErrRevoked)

	_, err = acquireClaim(t.Context(), &fakeService{acquireErr: errors.New("pool closed")}, "7", "uid-1")
	assert.EqualError(t, err, "pool closed")
}

// A request in flight when the claim is revoked answers the revocation, not the
// cut connection, and a revoked claim refuses the next.
func TestARevokedClaimCutsAndRefusesItsRequests(t *testing.T) {
	arrived := make(chan struct{})
	svc := serving(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
	}))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-1")
	require.NoError(t, err)
	defer c.close()
	revoked := make(chan struct{})
	c.revoked = revoked
	got := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)
		got <- err
	}()
	testutil.Wait(t, arrived, "the request to reach the server")

	close(revoked)

	assert.ErrorIs(t, testutil.Recv(t, got, "the request to end"), ErrRevoked)
	_, err = c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)
	assert.ErrorIs(t, err, ErrRevoked)
}

// A 401 or a revoked claim fails a whole read; a 403 or any other failure is one
// stream's.
func TestFatalIsTheUsersCredentialsOrTheCluster(t *testing.T) {
	assert.EqualError(t, fatal(&statusError{code: http.StatusUnauthorized, message: "Unauthorized"}), "the cluster refused to show this log: Unauthorized")
	assert.ErrorIs(t, fatal(ErrRevoked), ErrRevoked)
	assert.NoError(t, fatal(&statusError{code: http.StatusForbidden, message: "forbidden"}))
	assert.NoError(t, fatal(errors.New("connection reset")))
	assert.NoError(t, fatal(nil))
}

// A claim whose record's watch cannot open is refused, and releases its lease.
func TestAClaimWhoseWatchCannotOpenIsRefused(t *testing.T) {
	svc := serving(t, endless(nil))
	svc.watchErr = errors.New("no watch")
	_, err := openClaim(t.Context(), svc, "7", "uid-1", time.Millisecond)
	assert.EqualError(t, err, "no watch")
	assert.Equal(t, int32(1), svc.lease.released.Load())
}

// A record's watch that ends before the claim does revokes it: a claim nothing
// watches could outlive the cluster.
func TestAWatchThatEndsRevokesTheClaim(t *testing.T) {
	svc := serving(t, endless(nil))
	svc.frames = make(chan cluster.ClusterWatchFrame)
	c, err := openClaim(t.Context(), svc, "7", "uid-1", time.Millisecond)
	require.NoError(t, err)
	defer c.close()

	close(svc.frames)

	testutil.WaitClosed(t, c.revoked, "the claim to be revoked")
}

// deliver sends frame on frames, failing the test if nothing takes it.
func deliver(t *testing.T, frames chan<- cluster.ClusterWatchFrame, frame cluster.ClusterWatchFrame, what string) {
	t.Helper()
	select {
	case frames <- frame:
	case <-time.After(testutil.Timeout):
		t.Fatalf("timed out sending %s", what)
	}
}

// synced sends the record's snapshot on frames: the cluster, then the bookmark.
func synced(t *testing.T, frames chan<- cluster.ClusterWatchFrame) {
	t.Helper()
	deliver(t, frames, cluster.ClusterWatchFrame{Type: cluster.DeltaFrameAdded, Cluster: enabledCluster()}, "the record")
	deliver(t, frames, cluster.ClusterWatchFrame{Type: cluster.DeltaFrameBookmark}, "the bookmark")
}

// A record's watch that ends after its snapshot is opened again, and the claim
// stands while the new snapshot holds the record Kstack connects to.
func TestAWatchThatEndsIsOpenedAgain(t *testing.T) {
	svc := serving(t, endless(nil))
	first, second := make(chan cluster.ClusterWatchFrame), make(chan cluster.ClusterWatchFrame)
	svc.frames = first
	c, err := openClaim(t.Context(), svc, "7", "uid-1", time.Millisecond)
	require.NoError(t, err)
	defer c.close()
	synced(t, first)

	svc.set(func() { svc.frames = second })
	close(first)
	synced(t, second)

	assert.False(t, c.isRevoked())
	disabled := enabledCluster()
	disabled.Spec.Enabled = false
	deliver(t, second, cluster.ClusterWatchFrame{Type: cluster.DeltaFrameModified, Cluster: disabled}, "the record disabled")
	testutil.WaitClosed(t, c.revoked, "the claim to be revoked")
}

// A watch opened again whose snapshot lacks the record, or that cannot be opened,
// revokes the claim.
func TestAWatchOpenedAgainOnAGoneRecordRevokes(t *testing.T) {
	for name, gone := range map[string]func(svc *fakeService){
		"no record": func(svc *fakeService) {
			again := make(chan cluster.ClusterWatchFrame, 1)
			again <- cluster.ClusterWatchFrame{Type: cluster.DeltaFrameBookmark}
			svc.frames = again
		},
		"no watch": func(svc *fakeService) { svc.watchErr = errors.New("no watch") },
	} {
		t.Run(name, func(t *testing.T) {
			svc := serving(t, endless(nil))
			first := make(chan cluster.ClusterWatchFrame)
			svc.frames = first
			c, err := openClaim(t.Context(), svc, "7", "uid-1", time.Millisecond)
			require.NoError(t, err)
			defer c.close()
			synced(t, first)

			svc.set(func() { gone(svc) })
			close(first)

			testutil.WaitClosed(t, c.revoked, "the claim to be revoked")
		})
	}
}

// A cluster disabled under a follow ends the body being read, and refuses the
// next request.
func TestADisabledClusterEndsTheClaim(t *testing.T) {
	lines := make(chan string)
	svc := serving(t, endless(lines))
	svc.frames = make(chan cluster.ClusterWatchFrame)
	c, err := openClaim(t.Context(), svc, "7", "uid-1", time.Millisecond)
	require.NoError(t, err)
	defer c.close()
	body, err := c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", url.Values{"follow": {"true"}})
	require.NoError(t, err)
	defer body.Close()
	svc.frames <- cluster.ClusterWatchFrame{Type: cluster.DeltaFrameAdded, Cluster: enabledCluster()}

	disabled := enabledCluster()
	disabled.Spec.Enabled = false
	svc.frames <- cluster.ClusterWatchFrame{Type: cluster.DeltaFrameModified, Cluster: disabled}

	_, err = bufio.NewReader(body).ReadString('\n')
	assert.Error(t, err)
	testutil.WaitClosed(t, c.revoked, "the claim to be revoked")
	_, err = c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)
	assert.ErrorIs(t, err, ErrRevoked)
}

// The body goes on reading while the server goes on writing, and ends when the
// context does.
func TestTheBodyStreamsUntilTheContextEnds(t *testing.T) {
	lines := make(chan string)
	svc := serving(t, endless(lines))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-1")
	require.NoError(t, err)
	defer c.close()
	ctx, cancel := context.WithCancel(t.Context())
	body, err := c.get(ctx, "/api/v1/namespaces/prod/pods/web/log", url.Values{"follow": {"true"}})
	require.NoError(t, err)
	defer body.Close()
	r := bufio.NewReader(body)

	for _, want := range []string{"one\n", "two\n"} {
		lines <- want[:len(want)-1]
		got, err := r.ReadString('\n')
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	cancel()

	_, err = r.ReadString('\n')
	assert.Error(t, err)
}

// A status the API server refuses with is its code and its message, cleaned.
func TestARefusalIsItsStatus(t *testing.T) {
	svc := serving(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"kind":"Status","message":"container \"app\" in pod \"web\" is waiting to start: \u001b[31mContainerCreating"}`))
	}))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-1")
	require.NoError(t, err)
	defer c.close()

	_, err = c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)

	var se *statusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusBadRequest, se.code)
	assert.Equal(t, `container "app" in pod "web" is waiting to start: ContainerCreating`, se.message)
}

// A refusal whose body is no Status is named by its code alone.
func TestARefusalWithNoStatusIsItsCode(t *testing.T) {
	svc := serving(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream gone", http.StatusBadGateway)
	}))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-1")
	require.NoError(t, err)
	defer c.close()

	_, err = c.get(t.Context(), "/api/v1/namespaces/prod/pods/web/log", nil)

	assert.EqualError(t, err, "the API server answered 502")
}

// Close releases the lease.
func TestCloseReleasesTheLease(t *testing.T) {
	svc := serving(t, endless(nil))
	c, err := acquireClaim(t.Context(), svc, "7", "uid-1")
	require.NoError(t, err)

	c.close()

	assert.Equal(t, int32(1), svc.lease.released.Load())
}
