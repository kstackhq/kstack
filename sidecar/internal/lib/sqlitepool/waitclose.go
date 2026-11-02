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

package sqlitepool

import (
	"database/sql"
	"fmt"
	"time"
)

// CloseBudget is how long a caller about to delete a pool's file waits for its
// connections.
const CloseBudget = 5 * time.Second

// WaitClosed returns once every connection of the closed pools dbs has closed, or
// errors after budget. DB.Close closes only idle connections; one in use, as a
// cancelled transaction's rollback on database/sql's own goroutine, closes when it is
// let go, and Windows refuses to delete a file a connection still holds. A pool's
// stats keep counting down after its Close.
func WaitClosed(budget time.Duration, dbs ...*sql.DB) error {
	deadline := time.Now().Add(budget)
	for {
		n := 0
		for _, db := range dbs {
			n += db.Stats().OpenConnections
		}
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sqlitepool: %d connections still open", n)
		}
		time.Sleep(time.Millisecond)
	}
}
