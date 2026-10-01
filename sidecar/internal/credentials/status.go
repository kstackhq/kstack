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

package credentials

import (
	"cmp"
	"context"
	"crypto/sha256"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
)

type Status string

const (
	Valid   Status = "valid"   // borrowed, or discovered and not known to have expired
	Expired Status = "expired" // the tool said so, or a proxy's upstream refused it
	Missing Status = "missing" // no binary, or signed into nothing
	// Excluded is an identity the user excluded. Discover leaves it as it is.
	Excluded Status = "excluded"
)

// State is one identity's verdict: when it was entered, and the tool's one line
// through safe.String, "" for Valid.
type State struct {
	Key    Key
	Status Status
	Since  time.Time
	Detail string
}

// maxRefused is how many refused credentials a cache key remembers. A refreshed
// credential never hashes the same, so the oldest can go.
const maxRefused = 16

// refusal is what a cache key's upstream refused: the hashes and the values, the
// last maxRefused distinct ones, and whether the refusal still stands.
type refusal struct {
	hashes   [][sha256.Size]byte
	values   []string
	standing bool
}

// State is key's verdict, with no Status when the table holds none.
func (s *Store) State(key Key) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.status[key]; ok {
		return st
	}
	return State{Key: key}
}

// Subscribe is a gauge of the whole table: current on subscribe and again on
// every change. A delivery is shared by every receiver and is read-only.
func (s *Store) Subscribe() *watch.Receiver[[]State] {
	return s.hub.Receiver()
}

// MarkExpired is for a proxy whose upstream refused a credential it injected. arg
// is what it borrowed with, and sent the secret it sent. sent is refused for arg's
// cache key from then on. Unless the cache already holds a newer secret for that
// key, the identity's cache goes, its status is Expired, and the refusal stands
// until the tool answers another.
func (s *Store) MarkExpired(key Key, arg, sent string) {
	b := s.credentialBorrowing(key, arg)
	ck := b.key
	s.mu.Lock()
	defer s.mu.Unlock()
	s.borrowings[ck] = b
	r := s.refused[ck]
	if r == nil {
		r = &refusal{}
		s.refused[ck] = r
	}
	// A value refused again moves to the newest place, so concurrent reports of
	// one refusal cannot push another out.
	h := sha256.Sum256([]byte(sent))
	if i := slices.Index(r.hashes, h); i >= 0 {
		r.hashes = slices.Delete(r.hashes, i, i+1)
		r.values = slices.Delete(r.values, i, i+1)
	}
	r.hashes = lastN(append(r.hashes, h), maxRefused)
	r.values = lastN(append(r.values, sent), maxRefused)
	// The tool may hand the refused value to something else, so it stays blanked.
	safe.SetSecrets("refused:"+ck, r.values...)
	// A request signed before a refresh can be refused after it, and says nothing
	// about the credential that replaced it.
	if a, ok := s.cache[ck]; ok && a.secret != sent {
		return
	}
	for k, other := range s.borrowings {
		if other.id == key {
			delete(s.cache, k)
		}
	}
	r.standing = true
	s.expireLocked(key, refusedDetail)
}

// Recheck borrows the identity again past the cache and the backoff — each cache
// key it holds a standing refusal for, or else each credential it borrowed since
// start — and answers the state that leaves.
func (s *Store) Recheck(ctx context.Context, key Key) State {
	s.mu.Lock()
	again := s.standingLocked(key)
	if len(again) == 0 {
		for _, b := range s.borrowings {
			if b.id == key && b.proves {
				again = append(again, b)
			}
		}
	}
	for _, b := range again {
		delete(s.cache, b.key)
		delete(s.backoff, b.key)
	}
	s.mu.Unlock()
	if len(again) == 0 {
		again = s.firstBorrowing(key)
	}
	var wg sync.WaitGroup
	for _, b := range again {
		// The outcome is the status the borrow writes.
		wg.Go(func() { _, _ = s.borrow(ctx, b) })
	}
	wg.Wait()
	return s.State(key)
}

// firstBorrowing is the one credential an identity never borrowed has. An Azure
// login has no resource to name, so it has none.
func (s *Store) firstBorrowing(key Key) []borrowing {
	switch key.Provider {
	case AWS:
		return []borrowing{s.awsBorrowing(key.Name)}
	case GitHub:
		return []borrowing{s.githubBorrowing("github.com")}
	case Google:
		return []borrowing{s.googleBorrowing()}
	default:
		return nil
	}
}

func (s *Store) isRefusedLocked(ck, secret string) bool {
	r := s.refused[ck]
	return r != nil && secret != "" && slices.Contains(r.hashes, sha256.Sum256([]byte(secret)))
}

// standingLocked is the borrows of the identity's cache keys that hold a standing
// refusal, any of which keeps it Expired whatever another key answers.
func (s *Store) standingLocked(id Key) []borrowing {
	var standing []borrowing
	for ck, r := range s.refused {
		if b := s.borrowings[ck]; r.standing && b.id == id {
			standing = append(standing, b)
		}
	}
	return standing
}

// refusedDetail is the Expired detail of a credential the provider refused.
const refusedDetail = "the provider refused the credential"

// expireLocked sets key Expired and counts it, so a Discover that began earlier
// leaves it standing.
func (s *Store) expireLocked(key Key, detail string) {
	s.expiries++
	s.expiredAt[key] = s.expiries
	s.setLocked(key, Expired, detail)
}

// setLocked writes a verdict, publishing only a change, so a steady identity
// delivers nothing.
func (s *Store) setLocked(key Key, st Status, detail string) {
	if cur, ok := s.status[key]; ok && cur.Status == st && cur.Detail == detail {
		return
	}
	s.status[key] = State{Key: key, Status: st, Since: s.opt.now(), Detail: detail}
	s.publishLocked()
}

func (s *Store) removeLocked(key Key) {
	if _, ok := s.status[key]; !ok {
		return
	}
	delete(s.status, key)
	s.publishLocked()
}

func (s *Store) publishLocked() {
	_ = s.tx.Send(s.statesLocked()) // a closed hub drops it
}

// statesLocked is the table, by key.
func (s *Store) statesLocked() []State {
	return slices.SortedFunc(maps.Values(s.status), func(a, b State) int {
		return cmp.Or(cmp.Compare(a.Key.Provider, b.Key.Provider), cmp.Compare(a.Key.Name, b.Key.Name))
	})
}

func lastN[T any](s []T, n int) []T {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
