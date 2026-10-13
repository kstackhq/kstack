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
	"slices"

	"github.com/amorey/gochan/mpsc"
)

// fanIn gathers a follow's readings: each line goes out as its stream delivers
// it, in that container's order and across containers in arrival order, so one
// quiet container never holds back the rest. endedHub carries each reading as it
// ends. Both say which reading, since a stream cancelled and reopened on the same
// log can deliver a line, and ends, after its successor started. Both hubs stay
// open while no reading runs, since a follow's readings come and go until it ends.
type fanIn struct {
	linesHub *mpsc.Hub[headed]
	endedHub *mpsc.Hub[*streamRead]
}

func newFanIn() *fanIn {
	return &fanIn{
		linesHub: mpsc.New[headed](0, mpsc.WithKeepOpen()),
		endedHub: mpsc.New[*streamRead](0, mpsc.WithKeepOpen()),
	}
}

// add relays r's lines until it ends, then reports its end. Both stop once ctx
// ends, which is the follow's: a reading cancelled on its own still reports.
func (f *fanIn) add(ctx context.Context, r *streamRead) {
	lines, ended := f.linesHub.Sender(), f.endedHub.Sender()
	go func() {
		defer lines.Close()
		defer ended.Close()
		for rec := range r.lines.Chan() {
			if lines.SendContext(ctx, headed{rec, r}) != nil {
				drainReading(r)
				return
			}
		}
		_ = ended.SendContext(ctx, r) // an error is the follow ending, which no longer reads it
	}()
}

// handoff holds a follow's first lines, the ones that arrive while the page the
// client holds and the follow's opening reads overlap, and releases them sorted,
// as kubetail's runForwarder does.
type handoff struct {
	held     []Record
	released bool
}

// hold keeps rec while the handoff holds, and is false once it has been released,
// when rec goes out as it comes.
func (h *handoff) hold(rec Record) bool {
	if h.released {
		return false
	}
	h.held = append(h.held, rec)
	return true
}

// release is every held line in order; from then on hold keeps nothing.
func (h *handoff) release() []Record {
	h.released = true
	slices.SortStableFunc(h.held, compareLines)
	held := h.held
	h.held = nil
	return held
}
