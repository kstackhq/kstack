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

package cluster

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/internal/kubeconn"
)

// rateStart is the service clock serviceOver pins: where the gauge takes its first sample.
var rateStart = t0.Add(time.Hour)

// manualTicks drives the data-rate gauge's ticks and clock by hand and records the interval it
// asked for.
type manualTicks struct {
	ch       chan time.Time
	interval time.Duration
	elapsed  atomic.Int64 // the service clock, as a time.Duration past rateStart
}

// watchDataRate opens the gauge for id over a cancellable context, with ticks the test sends.
// The gauge has taken its first sample by the time it returns.
func watchDataRate(t *testing.T, d deps, id ClusterID, interval time.Duration) (<-chan ClusterDataRate, *manualTicks, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ticks := &manualTicks{ch: make(chan time.Time)}
	svc := serviceOver(t, d)
	svc.now = func() time.Time { return rateStart.Add(time.Duration(ticks.elapsed.Load())) }
	asked := make(chan time.Duration, 1)
	svc.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		asked <- d
		return ticks.ch, func() {}
	}
	ch, err := svc.Clusters().WatchDataRate(ctx, id, interval)
	require.NoError(t, err)
	ticks.interval = testutil.Recv(t, asked, "the gauge to arm its ticker")
	return ch, ticks, cancel
}

// tick moves the clock to start+after and fires a tick scheduled then. The channel is
// unbuffered, so it returns once the gauge has taken it.
func (m *manualTicks) tick(t *testing.T, after time.Duration) {
	t.Helper()
	m.tickLate(t, after, after)
}

// tickLate fires a tick scheduled for start+scheduled with the clock at start+now, as a tick
// that waited does.
func (m *manualTicks) tickLate(t *testing.T, scheduled, now time.Duration) {
	t.Helper()
	m.elapsed.Store(int64(now))
	select {
	case m.ch <- rateStart.Add(scheduled):
	case <-time.After(testutil.Timeout):
		t.Fatal("the gauge never took the tick")
	}
}

// echoConnection is a real connection to a server that answers each request with its body,
// so a test moves bytes both ways over the connection's own sockets.
func echoConnection(t *testing.T) *kubeconn.Connection {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	}))
	t.Cleanup(srv.Close)
	conn, err := kubeconn.NewConnection(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	t.Cleanup(conn.Retire)
	return conn
}

// move sends an n-byte body over conn and reads it back. The counts it moved by are the wire's,
// headers included, so a test reads them off conn.Bytes rather than expecting n.
func move(t *testing.T, conn *kubeconn.Connection, n int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, conn.BaseURL.String(), bytes.NewReader(make([]byte, n)))
	require.NoError(t, err)
	resp, err := conn.HTTPClient.Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// connectedTo is a cluster whose claim hands back conn.
func connectedTo(t *testing.T, conn *kubeconn.Connection) (deps, ClusterID) {
	t.Helper()
	d := newTestDeps(t)
	obj := createCluster(t, d, "prod")
	pool := &fakeKubeconn{}
	pool.conn.Store(conn)
	d.kubeconnSvc = pool
	return d, clusterIDOf(obj)
}

func TestClustersWatchDataRateReportsBytesPerSecond(t *testing.T) {
	conn := echoConnection(t)
	d, id := connectedTo(t, conn)
	ch, ticks, _ := watchDataRate(t, d, id, 0)

	move(t, conn, 100)
	received, sent := conn.Bytes()
	ticks.tick(t, 2*time.Second)

	want := ClusterDataRate{ReceivedBytesPerSecond: float64(received) / 2, SentBytesPerSecond: float64(sent) / 2}
	assert.Equal(t, want, testutil.Recv(t, ch, "the rate"))
}

// A tick that waited carries the time it was scheduled for; the rate divides by the time the
// counters were read at.
func TestClustersWatchDataRateDividesByTheTimeOfTheRead(t *testing.T) {
	conn := echoConnection(t)
	d, id := connectedTo(t, conn)
	ch, ticks, _ := watchDataRate(t, d, id, 0)

	move(t, conn, 100)
	received, sent := conn.Bytes()
	ticks.tickLate(t, 2*time.Second, 5*time.Second)

	want := ClusterDataRate{ReceivedBytesPerSecond: float64(received) / 5, SentBytesPerSecond: float64(sent) / 5}
	assert.Equal(t, want, testutil.Recv(t, ch, "the rate"))
}

// A gauge says nothing before its first measurement, and a rate needs two samples.
func TestClustersWatchDataRateSaysNothingBeforeTheFirstTick(t *testing.T) {
	d, id := connectedTo(t, echoConnection(t))
	ch, ticks, _ := watchDataRate(t, d, id, 0)

	// A negative assertion has no event to wait for, so it needs a bounded window.
	testutil.NoRecv(t, ch, quietWindow, "a rate before the first tick")

	ticks.tick(t, time.Second)
	assert.Zero(t, testutil.Recv(t, ch, "the first rate"))
}

// An unchanged rate is not news. The gauge takes the next tick only once it is done with the
// last, so a rate it had sent instead would leave it waiting on the reader and the tick untaken.
func TestClustersWatchDataRateSendsOnlyAMovedRate(t *testing.T) {
	d, id := connectedTo(t, echoConnection(t))
	ch, ticks, _ := watchDataRate(t, d, id, 0)

	ticks.tick(t, time.Second)
	assert.Zero(t, testutil.Recv(t, ch, "the idle rate"))

	ticks.tick(t, 2*time.Second)
	ticks.tick(t, 3*time.Second)
}

// A replacement counts from zero, so its first tick has no earlier sample of its own.
func TestClustersWatchDataRateStartsAgainOnANewConnection(t *testing.T) {
	old := echoConnection(t)
	d, id := connectedTo(t, old)
	pool := d.kubeconnSvc.(*fakeKubeconn)
	ch, ticks, _ := watchDataRate(t, d, id, 0)

	move(t, old, 100)
	ticks.tick(t, time.Second)
	assert.NotZero(t, testutil.Recv(t, ch, "the old connection's rate"))

	fresh := echoConnection(t)
	pool.conn.Store(fresh)
	move(t, fresh, 40)
	received0, sent0 := fresh.Bytes()
	ticks.tick(t, 2*time.Second)
	assert.Zero(t, testutil.Recv(t, ch, "the tick the connection changed on"))

	move(t, fresh, 40)
	received, sent := fresh.Bytes()
	ticks.tick(t, 3*time.Second)
	want := ClusterDataRate{ReceivedBytesPerSecond: float64(received - received0), SentBytesPerSecond: float64(sent - sent0)}
	assert.Equal(t, want, testutil.Recv(t, ch, "the new connection's rate"))
}

// No connection is not an error: the cluster is claimed and simply moving nothing.
func TestClustersWatchDataRateReadsNoConnectionAsZero(t *testing.T) {
	d := newTestDeps(t)
	obj := createCluster(t, d, "prod")
	d.kubeconnSvc = &fakeKubeconn{connErr: kubeconn.ErrNoConnection}
	ch, ticks, _ := watchDataRate(t, d, clusterIDOf(obj), 0)

	ticks.tick(t, time.Second)

	assert.Zero(t, testutil.Recv(t, ch, "the rate with no connection"))
}

func TestClustersWatchDataRateHoldsTheIntervalToItsRange(t *testing.T) {
	for _, tc := range []struct {
		asked, want time.Duration
	}{
		{0, defaultDataRateInterval},
		{-time.Second, defaultDataRateInterval},
		{time.Millisecond, time.Second},
		{5 * time.Second, 5 * time.Second},
		{time.Hour, time.Minute},
	} {
		d, id := connectedTo(t, echoConnection(t))
		_, ticks, _ := watchDataRate(t, d, id, tc.asked)
		assert.Equal(t, tc.want, ticks.interval, "asked for %v", tc.asked)
	}
}

func TestClustersWatchDataRateRefusesADisabledCluster(t *testing.T) {
	d := newTestDeps(t)
	obj := createCluster(t, d, "prod")
	_, err := serviceOver(t, d).Clusters().SetEnabled(context.Background(), clusterIDOf(obj), false)
	require.NoError(t, err)

	_, err = serviceOver(t, d).Clusters().WatchDataRate(context.Background(), clusterIDOf(obj), 0)

	assert.ErrorIs(t, err, ErrNotConnectable)
}

func TestClustersWatchDataRateReleasesItsClaimWhenTheStreamEnds(t *testing.T) {
	d, id := connectedTo(t, echoConnection(t))
	pool := d.kubeconnSvc.(*fakeKubeconn)
	ch, _, cancel := watchDataRate(t, d, id, 0)

	cancel()

	testutil.WaitClosed(t, ch, "the gauge to close on cancellation")
	assert.Equal(t, []string{"prod"}, pool.released)
}

func TestClustersWatchDataRateStopsForAConsumerThatIsGone(t *testing.T) {
	d, id := connectedTo(t, echoConnection(t))
	ch, ticks, cancel := watchDataRate(t, d, id, 0)

	ticks.tick(t, time.Second) // the gauge now waits on a reader that never comes
	cancel()

	testutil.WaitClosed(t, ch, "the gauge to give up its send")
}

func TestNewTickerTicks(t *testing.T) {
	ticks, stop := newTicker(time.Millisecond)
	t.Cleanup(stop)

	testutil.Recv(t, ticks, "a tick")
}
