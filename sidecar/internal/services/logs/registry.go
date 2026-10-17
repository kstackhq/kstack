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
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// registry is one log file's tailers: one per stream, shared by every follow that
// attaches to it and kept the service's grace after the last lets go. Every tailer is
// unlabelled, since the grep runs where the lines are read. While any runs, the
// registry holds a claim on the file and one on the cluster, which the tailers read
// over. It reconciles nothing: a tailer that ends is dropped, and the next attach
// starts one afresh. A registry lives as long as the service, since a follow holds
// its registry for its own life: one per cache id ever followed.
type registry struct {
	s       *Service
	cacheID cluster.ClusterCacheID

	mu      sync.Mutex
	tailers map[int64]*tailer
	// id and serverUID are the cluster the file was resolved against, as the last
	// attach with no tailer running found it, since a context can be repointed.
	id        cluster.ClusterID
	serverUID string

	// claimMu guards file, claim and backend, held while any tailer runs or starts and
	// nil otherwise; a start takes them outside mu, since taking them is slow.
	claimMu sync.Mutex
	file    *logstore.Store
	claim   *claim
	backend Backend
}

// registryFor is the registry of the file t was resolved against, on cluster id.
func (s *Service) registryFor(id cluster.ClusterID, t target) *registry {
	s.tailsMu.Lock()
	r := s.tails[t.cacheID]
	if r == nil {
		r = &registry{s: s, cacheID: t.cacheID, tailers: map[int64]*tailer{}}
		s.tails[t.cacheID] = r
	}
	s.tailsMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tailers) == 0 {
		r.id, r.serverUID = id, t.serverUID
	}
	return r
}

// tailOf is the start of the tailer running on the stream row of cacheID's file and
// how far it has written, both nil with none or before the tailer's start has run.
// Both are read under the one lock, so committed never runs ahead of the start it is
// read against.
func (s *Service) tailOf(cacheID cluster.ClusterCacheID, row int64) (start, committed *logstore.Pos) {
	s.tailsMu.Lock()
	r := s.tails[cacheID]
	s.tailsMu.Unlock()
	if r == nil {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.tailers[row]
	if t == nil || t.stopping || t.start.Load() == nil {
		return nil, nil
	}
	c := t.committed()
	return t.start.Load(), &c
}

// attach joins the tailer of st, the stream row of the file, starting it when none
// runs at the later of the stream's newest line and watermark. Release the handle.
func (r *registry) attach(ctx context.Context, st stream, row int64, watermark logstore.Pos) (*handle, error) {
	for {
		r.mu.Lock()
		t := r.tailers[row]
		switch {
		case t == nil:
			t = &tailer{r: r, st: st, row: row, done: make(chan struct{}), ready: make(chan struct{}), refs: 1}
			r.tailers[row] = t
			r.mu.Unlock()
			if err := r.start(ctx, t, watermark); err != nil {
				return nil, err
			}
			return &handle{t: t}, nil
		case !t.started():
			// Another attach is starting it.
			r.mu.Unlock()
			select {
			case <-t.ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case !t.stopping:
			t.join()
			r.mu.Unlock()
			return &handle{t: t}, nil
		}
		// Its chunk closes as it ends; one started now would race it.
		r.mu.Unlock()
		select {
		case <-t.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// start runs t, in tailers and not yet ready, from the later of its stream's newest
// line and watermark, over the registry's claims, taken when none runs. It runs with
// mu released: t's place in tailers keeps the claims held meanwhile, and the other
// attaches wait on ready. A start that fails drops t, and gives the claims back when
// no tailer is left.
func (r *registry) start(ctx context.Context, t *tailer, watermark logstore.Pos) error {
	defer close(t.ready)
	err := r.run(ctx, t, watermark)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.tailers, t.row)
		if len(r.tailers) == 0 {
			r.letGo()
		}
	}
	return err
}

// run takes the claims and starts t's read from the later of its stream's newest
// line and watermark.
func (r *registry) run(ctx context.Context, t *tailer, watermark logstore.Pos) error {
	if err := r.hold(ctx); err != nil {
		return err
	}
	chunks, err := r.file.Chunks(context.Background(), t.row, "")
	if err != nil {
		return err
	}
	start, prev := watermark, (*logstore.Pos)(nil)
	if newest := newestOf(chunks); newest != nil && newest.Compare(watermark) >= 0 {
		start, prev = *newest, newest
	}
	// A tailer in its grace has no follow to end it, so the file closing does.
	fileCtx, err := r.file.Context()
	if err != nil {
		return err
	}
	ctx, t.cancel = context.WithCancel(fileCtx)
	t.start.Store(&start)
	file, backend := r.file, r.backend
	release := file.Hold(t.row)
	first := t.read(ctx, backend, start)
	go t.run(ctx, file, backend, first, prev, release)
	return nil
}

// hold takes the claims the tailers run over, unless they are held.
func (r *registry) hold(ctx context.Context) error {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	if r.file != nil {
		return nil
	}
	file, err := r.s.files.OpenOrCreate(int64(r.cacheID))
	if err != nil {
		return err
	}
	c, err := openClaim(ctx, r.s.clusters, r.id, r.serverUID, r.s.retry)
	if err != nil {
		file.Close()
		return err
	}
	r.file, r.claim, r.backend = file, c, r.s.backends[0].build(c.get)
	return nil
}

// letGo gives back the claims once no tailer runs. It is called with mu held.
func (r *registry) letGo() {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	if r.file == nil {
		return
	}
	r.claim.close()
	_ = r.file.Close() // the claim's file closing is no failure of anything still running
	r.file, r.claim, r.backend = nil, nil, nil
}

// release lets go of one hold on t, which stops once its grace has passed with none.
func (r *registry) release(t *tailer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t.refs--
	if t.refs > 0 || t.stopping {
		return
	}
	if r.s.grace <= 0 {
		t.stop()
		return
	}
	t.grace = time.AfterFunc(r.s.grace, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if t.refs == 0 && !t.stopping {
			t.stop()
		}
	})
}

// ended drops t, which has ended, and lets go of the claims once no tailer runs.
func (r *registry) ended(t *tailer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Ended in its grace, the timer would fire on a tailer the registry has dropped.
	if t.grace != nil {
		t.grace.Stop()
		t.grace = nil
	}
	if r.tailers[t.row] == t {
		delete(r.tailers, t.row)
	}
	if len(r.tailers) == 0 {
		r.letGo()
	}
}

// handle is one follow's hold on a tailer.
type handle struct {
	t    *tailer
	once sync.Once
}

// Done closes once the tailer has ended, its last lines in the file; Err is then
// how its read ended.
func (h *handle) Done() <-chan struct{} { return h.t.done }
func (h *handle) Err() error            { return h.t.err }

// Start is where the tailer reads on from now: every gap of its stream past it is
// the tailer's.
func (h *handle) Start() logstore.Pos { return *h.t.start.Load() }

// Committed is how far the tailer has written into the file: the last line its tail
// has landed, or its start before that. It holds once the tailer has ended.
func (h *handle) Committed() logstore.Pos { return h.t.committed() }

// Release lets go of the hold; a second call does nothing.
func (h *handle) Release() { h.once.Do(func() { h.t.r.release(h.t) }) }
