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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// span, and every line is shown once.
func TestAWalkStopsAtALiveGap(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(100)...)
	svc, f := serviceOver(t, webMirror(t), s)
	f.chunk(64)
	svc.budget = 1 << 10

	got := pages(t, svc, web1View, FetchHead, MaxLimit)

	assert.Greater(t, len(got), 1, "the budget cuts the first page")
	assert.Equal(t, messages(linesOf(lineEvery(100))), recordsOf(got, FetchHead))
	assert.Len(t, logstore.CoverageOf(chunksOf(t, svc, f, "web-1", "app"), []string{"kubelet"}).Spans, 1,
		"each page's fill continues the chunk the one before it wrote")
}

// A walk crosses a lost gap as it crosses a seam: a Tail page that reached the
// kubelet's oldest line over a stream with an older chunk below reads on into that
// chunk and answers nothing older, and a Head page over a stream no backend serves
// reads past the gaps inside it, so no page answers more at one spot twice.
func TestAWalkCrossesALostGap(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)[5:]...)
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Lines: fileLines(0, time.Second)},
		logstore.Fill{Lines: fileLines(5*time.Second, 6*time.Second, 7*time.Second, 8*time.Second, 9*time.Second), Head: true})

	page, err := svc.Fetch(t.Context(), "7", web1View, FetchQuery{Mode: FetchTail, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "5", "6", "7", "8", "9"}, messages(page.Records))
	assert.Equal(t, PageEdge{}, page.Before)

	gone := View{Sources: []ViewSource{{Namespace: "prod", Kind: SourcePod, Name: "old-1"}}}
	seed(t, svc, f, rowOf("old-1", "app"),
		logstore.Fill{Lines: fileLines(0, time.Second)},
		logstore.Fill{Lines: fileLines(5 * time.Second)})
	page, err = svc.Fetch(t.Context(), "7", gone, FetchQuery{Mode: FetchHead, Limit: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "5"}, messages(page.Records))
	assert.False(t, page.After.More)
}
