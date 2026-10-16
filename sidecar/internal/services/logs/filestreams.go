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

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// pageStream is one stream a page reads: its file row, the stream the resolve
// handed out for it, nil for one only the file holds, and its lines' stream and
// place as the page answers them.
type pageStream struct {
	row   logstore.Stream
	st    *stream
	key   StreamKey
	place Place
}

// exhausted is whether no backend serves ps: it is read from the file alone, and
// has nothing older than its oldest line and nothing newer than its newest.
func (ps pageStream) exhausted() bool { return ps.st == nil }

// instance is ps's place in the order of lines at one moment.
func (ps pageStream) instance() instance {
	return instance{ps.row.Namespace, ps.row.Pod, ps.row.Container, ps.row.ContainerID}
}

// pageStreams files t's streams in file, each as its instance's row, and answers
// them.
func pageStreams(ctx context.Context, file *logstore.Store, t target) ([]pageStream, error) {
	rows := make([]logstore.Stream, len(t.streams))
	for i, st := range t.streams {
		rows[i] = logstore.Stream{
			Namespace: st.key.Namespace, Pod: st.key.Pod, PodUID: st.gen.uid,
			Container: st.key.Container, Default: st.isDefault, ContainerID: st.gen.instance,
			StartedAt: st.startedAt, FinishedAt: st.finishedAt,
			Node: st.place.Node, Region: st.place.Region, Zone: st.place.Zone, OS: st.place.OS, Arch: st.place.Arch,
			Labels: st.labels,
		}
	}
	ids, err := file.PutStreams(ctx, rows)
	if err != nil {
		return nil, err
	}
	// A restart the mirror reported late may have filed the next instance's lines
	// under the one before: the file's row of that one is trimmed once its end is known.
	var finished []logstore.Stream
	for _, st := range t.streams {
		if st.previous != "" && st.previousFinishedAt != 0 {
			finished = append(finished, logstore.Stream{
				Namespace: st.key.Namespace, Pod: st.key.Pod, Container: st.key.Container,
				ContainerID: st.previous, FinishedAt: st.previousFinishedAt,
			})
		}
	}
	if len(finished) > 0 {
		if err := file.FinishStreams(ctx, finished); err != nil {
			return nil, err
		}
	}
	out := make([]pageStream, len(t.streams))
	for i, st := range t.streams {
		rows[i].ID = ids[i]
		out[i] = pageStream{row: rows[i], st: &t.streams[i], key: st.key, place: st.place}
	}
	return out, nil
}
