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

package logstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Evict deletes the chunk id and the lines it claims that no other chunk does, in
// one transaction, so a reader never sees a chunk without its lines. The chunk's
// bounds are read in the same transaction, since a fill may have extended it since
// the caller looked. A chunk linked to it starts a span from then on, since the lines
// before it are gone.
func (s *Store) Evict(ctx context.Context, id int64) error {
	err := s.inTx(ctx, func(ctx context.Context, st stmts) error {
		var stream int64
		var c Chunk
		err := st.QueryRow(ctx, stmtSelectChunkBounds, id).Scan(&stream, &c.From.TS, &c.From.Ord, &c.To.TS, &c.To.Ord)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		if _, err := st.Exec(ctx, stmtEvictLines, stream, c.From.TS, c.From.Ord, c.To.TS, c.To.Ord, id); err != nil {
			return err
		}
		_, err = st.Exec(ctx, stmtEvictChunk, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("evict: %w", err)
	}
	return nil
}
