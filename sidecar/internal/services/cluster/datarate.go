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

// The data-rate gauge: how fast a cluster's connection is moving bytes on the wire.
package cluster

import (
	"context"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/internal/kubeconn"
)

// ClusterDataRate is a connection's throughput over the last sampling interval, in bytes per
// second. Mirrors the schema type.
type ClusterDataRate struct {
	ReceivedBytesPerSecond float64 `json:"receivedBytesPerSecond"`
	SentBytesPerSecond     float64 `json:"sentBytesPerSecond"`
}

// The sampling interval's default and the range a caller's is held to.
const (
	defaultDataRateInterval = 2 * time.Second
	minDataRateInterval     = time.Second
	maxDataRateInterval     = time.Minute
)

// dataRateInterval holds a caller's interval to the range; zero or less is the default.
func dataRateInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultDataRateInterval
	}
	return min(max(d, minDataRateInterval), maxDataRateInterval)
}

// newTicker is time.NewTicker as the gauge's tick source.
func newTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// WatchDataRate streams the rate of the cluster's connection: one sample per interval, sent only
// when the rate moved. The first value arrives one interval after subscribing, since a rate
// needs two samples.
//
// Each tick reads whichever connection the claim holds. No connection reads as zero, and so does
// the tick a connection is replaced on: the new one counts from zero.
//
// The claim is this stream's own, released when ctx ends, and refuses what AcquireConnection
// refuses.
func (a clustersAPI) WatchDataRate(ctx context.Context, id ClusterID, interval time.Duration) (<-chan ClusterDataRate, error) {
	lease, err := a.s.AcquireConnection(ctx, id)
	if err != nil {
		return nil, err
	}

	prev := sampleBytes(ctx, lease, a.s.now())
	ticks, stop := a.s.newTicker(dataRateInterval(interval))
	out := make(chan ClusterDataRate)
	go func() {
		defer close(out)
		defer lease.Release()
		defer stop()

		var last ClusterDataRate
		sent := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
			}

			// The read's own time, not the tick's: a tick that waited on a slow reader carries
			// the time it was scheduled for.
			cur := sampleBytes(ctx, lease, a.s.now())
			rate := cur.rateSince(prev)
			prev = cur
			if sent && rate == last {
				continue
			}
			if !sendFrame(ctx, out, rate) {
				return
			}
			last, sent = rate, true
		}
	}()
	return out, nil
}

// byteSample is one reading of a connection's counters. A zero seq is no connection.
type byteSample struct {
	seq      uint64
	received uint64
	sent     uint64
	at       time.Time
}

func sampleBytes(ctx context.Context, lease kubeconn.Lease, at time.Time) byteSample {
	conn, err := lease.Conn(ctx)
	if err != nil {
		return byteSample{at: at}
	}
	received, sent := conn.Bytes()
	return byteSample{seq: conn.Seq(), received: received, sent: sent, at: at}
}

// rateSince is the rate between two samples of the same connection, and zero otherwise.
func (s byteSample) rateSince(prev byteSample) ClusterDataRate {
	elapsed := s.at.Sub(prev.at).Seconds()
	if s.seq == 0 || s.seq != prev.seq || elapsed <= 0 {
		return ClusterDataRate{}
	}
	return ClusterDataRate{
		ReceivedBytesPerSecond: float64(s.received-prev.received) / elapsed,
		SentBytesPerSecond:     float64(s.sent-prev.sent) / elapsed,
	}
}
