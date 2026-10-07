// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !windows

package chat

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustercard"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// A question's context block, pinned as the model reads it in testdata/context.md:
// the card, the notes, the workspace and the sandbox, in that order, in its tags,
// then the question. The sample is one cluster with a cache, two notes and two
// granted folders. `make prompts` rewrites it, beside the system prompt's goldens
// in internal/app. Unix alone, since the paths it names are Unix paths.
func TestTheContextBlockMatchesItsGolden(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	s := &service{
		sandboxStatus: sandbox.Status{Available: true, NetworkAvailable: true},
		memories:      sampleMemories{},
		chatsRoot:     root,
	}
	chatID := ChatID("0195f1a0-5b6e-7c3d-8e4f-0a1b2c3d4e5f")

	block := s.withMemory(t.Context(), clustercard.Render(sampleFacts()), "cluster-1")
	block = s.withSandbox(s.withWorkspace(block, chatID), sandboxState{
		networkThisTurn: true,
		folders:         []session.Folder{{Path: "/home/ana/src/payments", Write: true}, {Path: "/home/ana/notes"}},
	})
	// The chats' root is wherever the test runs; the golden names a home.
	block = strings.ReplaceAll(block, root.Name(), "/home/ana/.local/share/kstack/chats")
	got := llm.Prompt([]llm.Block{llm.ContextBlock(block), llm.TextBlock("Why is the payments deployment paused?")}) + "\n"

	path := filepath.Join("testdata", "context.md")
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run `make prompts` to write it")
	assert.Equal(t, string(want), got)
}

// sampleMemories is two notes as memory.Section writes them: the user's for the
// cluster, and one Kstack kept for every cluster.
type sampleMemories struct{}

func (sampleMemories) Section(context.Context, apimeta.ClusterID) json.RawMessage {
	return json.RawMessage(`{"today":"2026-03-15","memories":[` +
		`{"name":"deploy-window","scope":"cluster","by":"user","updated":"2026-02-02","body":"Never restart payments pods between 09:00 and 11:00 UTC."},` +
		`{"name":"istio-apis","scope":"everywhere","by":"model","updated":"2026-01-20","body":"Istio 1.24 serves both the v1 and v1beta1 VirtualService APIs."}]}`)
}

// sampleFacts is one reachable cluster with a cache: three namespaces, four
// built-in kinds, one of them paused, and Istio's.
func sampleFacts() clustercard.Facts {
	return clustercard.Facts{
		Name:       "prod",
		Context:    "gke_acme_us-east1_prod",
		Version:    "v1.31.2",
		Connection: "Connected",
		TLS:        "verified",
		Cache: &clustercard.CacheFacts{
			Health:    cluster.ClusterCacheHealth{Reason: "Watching", TotalKinds: 5, PausedKinds: 1},
			Discovery: "Discovered",
			Kinds: []clustercard.Kind{
				{APIVersion: "v1", Kind: "Pod", Resource: "pods", Scope: "Namespaced", Reason: "Watching"},
				{APIVersion: "v1", Kind: "Namespace", Resource: "namespaces", Scope: "Cluster", Reason: "Watching"},
				{APIVersion: "v1", Kind: "Node", Resource: "nodes", Scope: "Cluster", Reason: "Watching"},
				{APIVersion: "apps/v1", Kind: "Deployment", Resource: "deployments", Scope: "Namespaced", Reason: "Paused"},
				{APIVersion: "networking.istio.io/v1", Kind: "VirtualService", Resource: "virtualservices", Scope: "Namespaced", Reason: "Watching"},
			},
			Namespaces: []string{"kube-system", "default", "payments"},
		},
	}
}
