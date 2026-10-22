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

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// maxInstances is the most of a container's instances a page reads from the file
// beside what the resolve hands out: a pod that crash-looped for days can hold
// hundreds, each a walk of its own.
const maxInstances = 8

// pageStream is one stream a page reads: its file row, the stream the resolve
// handed out for it, nil for one only the file holds, its lines' stream and place as
// the page answers them, and the start of the tailer running on it, nil with none.
type pageStream struct {
	row   logstore.Stream
	st    *stream
	key   StreamKey
	place Place
	// tail is the start of the tailer running on the stream and committed how far it
	// had written when the page began, both nil with none.
	tail      *logstore.Pos
	committed *logstore.Pos
}

// exhausted is whether no backend serves ps: it is read from the file alone, and
// has nothing older than its oldest line and nothing newer than its newest.
func (ps pageStream) exhausted() bool { return ps.st == nil }

// instance is ps's place in the order of lines at one moment.
func (ps pageStream) instance() instance {
	return instance{ps.row.Namespace, ps.row.Pod, ps.row.Container, ps.row.ContainerID}
}

// pageStreams files t's streams in file, each as its instance's row, and answers
// them, then each of view's Pod sources' streams the file holds that the resolve
// handed out for no source, each once across the view. Every row is held from the
// moment its id is known until release, which the caller owes even on an error.
func pageStreams(ctx context.Context, file *logstore.Store, t target, scope Scope) (_ []pageStream, release func(), err error) {
	var releases []func()
	release = func() {
		for _, r := range releases {
			r()
		}
	}
	rows, err := fileStreams(ctx, file, t.streams)
	if err != nil {
		return nil, release, err
	}
	releases = append(releases, file.Hold(rowIDs(rows)...))
	out := make([]pageStream, len(t.streams))
	seen := map[int64]bool{}
	for i, st := range t.streams {
		seen[rows[i].ID] = true
		out[i] = pageStream{row: rows[i], st: &t.streams[i], key: st.key, place: st.place}
	}
	for _, src := range scope.Sources {
		if src.Kind != SourcePod {
			continue
		}
		held, err := file.PodStreams(ctx, src.Namespace, src.Name, t.pods[src.Namespace+"/"+src.Name])
		if err != nil {
			return nil, release, err
		}
		releases = append(releases, file.Hold(rowIDs(held)...))
		key := src.Namespace + "/" + src.Name
		_, live := t.pods[key]
		pod := podNow{live: live, def: t.defaults[key], place: t.places[key]}
		for _, row := range fileRows(held, src, scope.Filters, t.current, pod) {
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			current := t.current[containerKey(row.Namespace, row.Pod, row.Container)]
			out = append(out, pageStream{
				row:   row,
				key:   StreamKey{Namespace: row.Namespace, Pod: row.Pod, Container: row.Container, Previous: row.ContainerID != current},
				place: pod.placeOf(row),
			})
		}
	}
	return out, release, nil
}

// rowIDs is the id of each of rows.
func rowIDs(rows []logstore.Stream) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

// fileStreams files streams in file, each as its instance's row, and answers the rows
// with their ids, in order. A restart the mirror reported late may have filed the
// next instance's lines under the one before, so the file's row of that one is
// trimmed once its end is known.
func fileStreams(ctx context.Context, file *logstore.Store, streams []stream) ([]logstore.Stream, error) {
	rows := make([]logstore.Stream, len(streams))
	for i, st := range streams {
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
	for i := range rows {
		rows[i].ID = ids[i]
	}
	var finished []logstore.Stream
	for _, st := range streams {
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
	return rows, nil
}

// podNow is what the mirror says of a Pod source's pod now: whether it holds the pod,
// and if so its default container and where it runs. A gone pod's rows keep what was
// filed of them.
type podNow struct {
	live  bool
	def   string
	place Place
}

// placeOf is where row's instance ran: where the pod runs now while the mirror holds
// it, since a node's labels can move, else where it was filed.
func (p podNow) placeOf(row logstore.Stream) Place {
	if p.live {
		return p.place
	}
	return Place{Node: row.Node, Region: row.Region, Zone: row.Zone, OS: row.OS, Arch: row.Arch}
}

// isDefault is whether row's container is the pod's default: the one the mirror names
// while it holds the pod, since its annotation can move, else the rows marked so.
func (p podNow) isDefault(row logstore.Stream) bool {
	if p.live {
		return row.Container == p.def
	}
	return row.Default
}

// fileRows is the rows of held, a Pod source's streams in the file newest first per
// container, that src reads: the containers it names, every one, or the pod's
// default, as the resolve names them; with Previous, not the pod's current instance;
// in a place the filters keep; and each container's newest maxInstances.
func fileRows(held []logstore.Stream, src ScopeSource, filters []ScopeFilter, current map[string]string, pod podNow) []logstore.Stream {
	var out []logstore.Stream
	count := map[string]int{}
	for _, row := range held {
		switch {
		case len(src.Containers) > 0 && !slices.Contains(src.Containers, row.Container),
			len(src.Containers) == 0 && !src.AllContainers && !pod.isDefault(row),
			src.Previous && row.ContainerID == current[containerKey(row.Namespace, row.Pod, row.Container)],
			!keptByFilters(pod.placeOf(row), filters):
			continue
		}
		if count[row.Container] == maxInstances {
			continue
		}
		count[row.Container]++
		out = append(out, row)
	}
	return out
}

// keptByFilters is whether every filter keeps an instance that ran in place.
func keptByFilters(place Place, filters []ScopeFilter) bool {
	for _, f := range filters {
		var value string
		switch f.Field {
		case FilterNode:
			value = place.Node
		case FilterRegion:
			value = place.Region
		case FilterZone:
			value = place.Zone
		case FilterOS:
			value = place.OS
		case FilterArch:
			value = place.Arch
		}
		if !slices.Contains(f.Values, value) {
			return false
		}
	}
	return true
}
