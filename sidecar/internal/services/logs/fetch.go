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
	"fmt"
	"regexp"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// MaxLimit is the most lines one page holds.
const MaxLimit = 500

// A fetch's bounds: the bytes read across its streams, shared evenly so no stream
// is starved by the others, and the GETs each stream may send. An ordinary page
// never reaches either; a rare grep read backward does.
const (
	maxBudget  = 32 << 20
	maxWindows = 16
)

// FetchQuery is which page of a view to read: the end cut from when the range
// holds more than Limit lines, and the cursors bounding it. Since and Until keep
// their cursor's line, After and Before do not. With no cursor, Head reads from the
// oldest line and Tail from the newest.
type FetchQuery struct {
	Mode   FetchMode
	Since  *string
	Until  *string
	After  *string
	Before *string
	Limit  int
}

// Fetch is one page of view's lines on cluster id. The sources are resolved inside
// one reading of the cluster's mirror; the reads run outside it, over a claim on
// the server the pods came from.
func (s *Service) Fetch(ctx context.Context, id cluster.ClusterID, view View, q FetchQuery) (Page, error) {
	if q.Limit < 1 || q.Limit > MaxLimit {
		return Page{}, Refusal{fmt.Sprintf("a limit must be between 1 and %d", MaxLimit)}
	}
	o, err := readOptsOf(view, q)
	if err != nil {
		return Page{}, err
	}
	t, err := resolve(ctx, s.clusters, id, view, false)
	if err != nil {
		return Page{}, err
	}
	if len(t.streams) == 0 {
		return Page{Records: []Record{}, Skipped: []string{}}, nil
	}
	c, err := acquireClaim(ctx, s.clusters, id, t.serverUID)
	if err != nil {
		return Page{}, err
	}
	defer c.close()
	backend := s.backend(c.get)
	o.batch, o.windows = int64(q.Limit)+1, maxWindows
	readings := make([]*streamRead, len(t.streams))
	for i, st := range t.streams {
		o.budget = newBudget(s.budget / int64(len(t.streams)))
		if q.Mode == FetchTail {
			readings[i] = backend.Backward(ctx, st, o)
		} else {
			readings[i] = backend.Forward(ctx, st, o)
		}
	}
	return mergePage(ctx, readings, q.Mode, q.Limit)
}

// readOptsOf is the bounds and grep a page reads with.
func readOptsOf(view View, q FetchQuery) (readOpts, error) {
	var o readOpts
	var err error
	if o.since, err = edge(q.Since, q.After); err != nil {
		return readOpts{}, err
	}
	if o.until, err = edge(q.Until, q.Before); err != nil {
		return readOpts{}, err
	}
	if view.Grep != "" {
		// The tool refused a pattern that does not compile, so one here is a plain refusal.
		if o.grep, err = regexp.Compile(view.Grep); err != nil {
			return readOpts{}, Refusal{"the grep is not a regular expression"}
		}
	}
	return o, nil
}

// edge is the bound one end of a range reads with: the cursor kept keeping its
// line, the cursor dropped dropping it, the second winning when both are set, and
// zero with neither.
func edge(kept, dropped *string) (bound, error) {
	cursor, incl := dropped, false
	if cursor == nil {
		cursor, incl = kept, true
	}
	if cursor == nil {
		return bound{}, nil
	}
	p, err := parseCursor(*cursor)
	return bound{p, incl}, err
}
