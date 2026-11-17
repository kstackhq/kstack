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
	"context"
	"sync/atomic"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
)

// Stream is a watch: its frames, and why they stopped. Frames closes on every
// exit, so Err is what tells a failure from an ordinary teardown.
type Stream struct {
	Frames <-chan Settings
	err    atomic.Pointer[error]
}

// Err returns why the stream ended, or nil if it ended cleanly. Read it once
// Frames has closed.
func (s *Stream) Err() error {
	if p := s.err.Load(); p != nil {
		return *p
	}
	return nil
}

func (s *service) Watch(ctx context.Context) (*Stream, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	ch := make(chan Settings, 1)
	st := &Stream{Frames: ch}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	go func() {
		defer s.wg.Done()
		defer close(ch)
		defer stop()
		defer cancel()
		if err := s.pump(ctx, ch); err != nil && ctx.Err() == nil {
			st.err.Store(&err)
		}
	}()
	return st, nil
}

// pump re-reads on its own key and on the clusters key, whose delete clears the
// default, and sends a row that differs from the last one sent.
func (s *service) pump(ctx context.Context, out chan<- Settings) error {
	// Subscribe, then read: a row read first would miss a commit in between.
	rx := s.db.Subscribe(appdb.KeyAppSettings, appdb.KeyClusters)
	defer rx.Close()

	var sent *Settings
	for {
		cur, err := s.Get(ctx)
		if err != nil {
			return err
		}
		if sent == nil || !same(*sent, cur) {
			select {
			case out <- cur:
				sent = &cur
			case <-ctx.Done(): // coverage:ignore only the subscriber leaving while a frame is in flight
				return nil
			}
		}
		if _, err := rx.RecvContext(ctx); err != nil {
			return nil
		}
	}
}

// same compares the ids by value: two reads of one row hold different pointers.
func same(a, b Settings) bool {
	if a.DefaultClusterID == nil || b.DefaultClusterID == nil {
		return a.DefaultClusterID == b.DefaultClusterID
	}
	return *a.DefaultClusterID == *b.DefaultClusterID
}
