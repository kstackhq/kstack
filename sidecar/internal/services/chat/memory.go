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

package chat

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/internal/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
)

// Memories is the notes a chat's cluster sees, as the section its context block
// carries.
type Memories interface {
	Section(ctx context.Context, clusterID apimeta.ClusterID) json.RawMessage
}

// withMemory is card with the cluster's notes appended as their own section,
// or card alone when the service holds no Memories.
func (s *service) withMemory(ctx context.Context, card string, clusterID apimeta.ClusterID) string {
	if s.memories == nil {
		return card
	}
	text, err := clustercard.WithSection(card, "Memory", s.memories.Section(ctx, clusterID))
	if err != nil {
		slog.Warn("memory section unreadable", "err", err)
		text, _ = clustercard.WithSection(card, "Memory", clustercard.UnavailableSection)
	}
	return text
}
