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

// Package credentials borrows credentials from the user's tools — aws, gh, gcloud
// and az — by running each tool's own command on the host, and keeps what they
// answer in memory until it expires. Nothing is written to disk, and every secret
// is registered with safe the moment it is read.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amorey/gochan/watch"
	"golang.org/x/sync/singleflight"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
)

// Provider names one tool's credential. The strings are the identity id's first
// part, the one spelling settings and the wire use.
type Provider string

const (
	AWS    Provider = "aws"
	GitHub Provider = "github"
	Google Provider = "gcp"
	Azure  Provider = "azure"
)

// providers is every provider, in the order Found lists them.
var providers = []Provider{AWS, GitHub, Google, Azure}

// Key names one identity: the provider and, for AWS, the profile.
type Key struct {
	Provider Provider
	Name     string
}

// String is "aws:dev", or the provider alone ("github", and "aws" for the bare
// key a tool that is missing or signed into nothing is reported under).
func (k Key) String() string {
	if k.Name == "" {
		return string(k.Provider)
	}
	return string(k.Provider) + ":" + k.Name
}

// Binaries is where each tool is, "" for one not found. The caller decides.
type Binaries struct{ AWS, GH, Gcloud, Az string }

const (
	borrowTimeout  = 30 * time.Second
	expiredBackoff = 30 * time.Second
	defaultTTL     = 15 * time.Minute
	githubTTL      = time.Hour
	// expiryMargin is how long before a credential's own expiry the cache lets it
	// go, so a request signed with it does not arrive after it ends.
	expiryMargin = time.Minute
)

var (
	ErrExpired  = errors.New("credentials: expired")
	ErrNoCLI    = errors.New("credentials: tool not installed")
	ErrExcluded = errors.New("credentials: excluded")
	ErrClosed   = errors.New("credentials: closed")
)

// runner runs one tool with env, nil for the sidecar's own environment: its output,
// and its exit code when it ran to an exit. err is for a run that did not: it could
// not start, or its context ended.
type runner func(ctx context.Context, env []string, bin string, args ...string) (stdout, stderr []byte, exit int, err error)

type options struct {
	now     func() time.Time
	timeout time.Duration
	backoff time.Duration
	run     runner
}

type option func(*options)

func withClock(now func() time.Time) option { return func(o *options) { o.now = now } }
func withTimeout(d time.Duration) option    { return func(o *options) { o.timeout = d } }
func withBackoff(d time.Duration) option    { return func(o *options) { o.backoff = d } }
func withRunner(r runner) option            { return func(o *options) { o.run = r } }

// Store borrows credentials from the user's tools and keeps them in memory.
type Store struct {
	bins     map[Provider]string
	contexts func() []string
	excluded func(Key) bool
	opt      options

	// ctx is what every tool run descends from; Close cancels it, then waits out
	// runs, so no tool outlives it. closing, under mu, refuses a new run.
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closing   bool
	runs      sync.WaitGroup
	flight    singleflight.Group

	mu         sync.Mutex
	cache      map[string]answer
	backoff    map[string]time.Time // a cache key's expiry, remembered until then
	borrowings map[string]borrowing // every cache key borrowed or refused since start
	refused    map[string]*refusal  // by cache key
	status     map[Key]State
	// expiries counts every expiry and refusal written; expiredAt is the count at an
	// identity's latest, so Discover can tell one written since it began.
	expiries  uint64
	expiredAt map[Key]uint64
	found     Found

	foundDone chan struct{}
	foundOnce sync.Once

	hub *watch.Hub[[]State]
	tx  *watch.Sender[[]State]
}

// NewStore borrows from the tools b names, running each in home. contexts is the
// kube context names, listed by Discover. excluded is read on every borrow, nil
// excluding nothing, and must be safe for concurrent use.
func NewStore(b Binaries, home string, contexts func() []string, excluded func(Key) bool) *Store {
	return newStoreWithOptions(b, home, contexts, excluded)
}

func newStoreWithOptions(b Binaries, home string, contexts func() []string, excluded func(Key) bool, opts ...option) *Store {
	o := options{now: time.Now, timeout: borrowTimeout, backoff: expiredBackoff, run: execRun(home, waitDelay)}
	for _, opt := range opts {
		opt(&o)
	}
	// A tool run with no home finds no config, so none is run.
	if home == "" {
		b = Binaries{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	hub := watch.New([]State{})
	return &Store{
		bins:       map[Provider]string{AWS: b.AWS, GitHub: b.GH, Google: b.Gcloud, Azure: b.Az},
		contexts:   contexts,
		excluded:   excluded,
		opt:        o,
		ctx:        ctx,
		cancel:     cancel,
		cache:      map[string]answer{},
		backoff:    map[string]time.Time{},
		borrowings: map[string]borrowing{},
		refused:    map[string]*refusal{},
		status:     map[Key]State{},
		expiredAt:  map[Key]uint64{},
		foundDone:  make(chan struct{}),
		hub:        hub,
		tx:         hub.Sender(),
	}
}

// Close stops every tool run in flight, returning once each has been reaped, and
// makes every later borrow answer ErrClosed.
func (s *Store) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.cancel()
		s.runs.Wait()
		s.hub.Close()
	})
}

// run is the one caller of the runner, counted in runs.
func (s *Store) run(ctx context.Context, env []string, bin string, args ...string) ([]byte, []byte, int, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, nil, -1, ErrClosed
	}
	s.runs.Add(1)
	s.mu.Unlock()
	defer s.runs.Done()
	return s.opt.run(ctx, env, bin, args...)
}

// answer is one borrow's result as the cache keeps it: the value, when the cache
// lets it go, the secret a proxy sends with it (hashed against refusals), and
// every secret it holds.
type answer struct {
	value   any
	expires time.Time
	secret  string
	secrets []string
}

// borrowing is one borrow: its cache key, the identity it answers for, whether a
// success proves the identity's login works, and how it asks the tool.
type borrowing struct {
	key    string
	id     Key
	proves bool
	fetch  func(ctx context.Context) (answer, error)
}

// tokenAnswer is the answer for a bearer token: one secret, which is what a proxy
// sends.
func tokenAnswer(value any, token string, expires time.Time) answer {
	return answer{value: value, expires: expires, secret: token, secrets: []string{token}}
}

// borrowAs is borrow's value, as the type b's fetch answers.
func borrowAs[T any](ctx context.Context, s *Store, b borrowing) (T, error) {
	a, err := s.borrow(ctx, b)
	if err != nil {
		var zero T
		return zero, err
	}
	return a.value.(T), nil
}

// credentialBorrowing is the credential borrow of an identity with the argument a
// proxy borrowed it with: the AWS profile, the GitHub host, the Azure resource, or
// nothing for Google.
func (s *Store) credentialBorrowing(key Key, arg string) borrowing {
	switch key.Provider {
	case AWS:
		return s.awsBorrowing(arg)
	case GitHub:
		return s.githubBorrowing(arg)
	case Azure:
		return s.azureBorrowing(arg)
	default:
		return s.googleBorrowing()
	}
}

// borrow answers b's cached value, or runs b.fetch once for every caller waiting
// on b.key. The run is bounded on a context of the store's, never a caller's, so
// a caller that gives up ends its own wait alone.
func (s *Store) borrow(ctx context.Context, b borrowing) (answer, error) {
	if s.ctx.Err() != nil {
		return answer{}, ErrClosed
	}
	if s.excluded != nil && s.excluded(b.id) {
		return answer{}, ErrExcluded
	}
	if s.bins[b.id.Provider] == "" {
		s.mu.Lock()
		s.setLocked(Key{Provider: b.id.Provider}, Missing, "")
		s.mu.Unlock()
		return answer{}, ErrNoCLI
	}
	s.mu.Lock()
	s.borrowings[b.key] = b
	a, done, err := s.cachedLocked(b.key)
	s.mu.Unlock()
	if done {
		return a, err
	}
	ch := s.flight.DoChan(b.key, func() (any, error) {
		// A caller arriving as the last flight ended finds its answer here.
		s.mu.Lock()
		a, done, err := s.cachedLocked(b.key)
		s.mu.Unlock()
		if done {
			return a, err
		}
		runCtx, cancel := context.WithTimeout(s.ctx, s.opt.timeout)
		defer cancel()
		a, err = b.fetch(runCtx)
		return s.settle(b, a, err)
	})
	select {
	case r := <-ch:
		return r.Val.(answer), r.Err
	case <-ctx.Done():
		return answer{}, ctx.Err()
	}
}

func (s *Store) cachedLocked(key string) (answer, bool, error) {
	now := s.opt.now()
	if a, ok := s.cache[key]; ok && now.Before(a.expires) {
		return a, true, nil
	}
	if now.Before(s.backoff[key]) {
		return answer{}, true, ErrExpired
	}
	return answer{}, false, nil
}

// settle stores what a run answered. It runs under the lock, so a refusal that
// landed while the run was in flight still applies to its answer.
func (s *Store) settle(b borrowing, a answer, err error) (answer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exp *expiry
	switch {
	case errors.As(err, &exp):
		s.backoff[b.key] = s.opt.now().Add(s.opt.backoff)
		s.expireLocked(b.id, exp.detail)
		return answer{}, ErrExpired
	case err != nil:
		var exit *exitError
		if errors.As(err, &exit) {
			slog.Info("credentials: a tool failed", "tool", exit.tool, "exit", exit.code)
		}
		return answer{}, err
	}
	if s.isRefusedLocked(b.key, a.secret) {
		// The tool is back to a credential the provider refused, whatever it
		// answered in between.
		s.backoff[b.key] = s.opt.now().Add(s.opt.backoff)
		s.refused[b.key].standing = true
		s.expireLocked(b.id, refusedDetail)
		return answer{}, ErrExpired
	}
	if old, ok := s.cache[b.key]; !ok || !slices.Equal(old.secrets, a.secrets) {
		safe.SetSecrets(b.key, a.secrets...)
	}
	s.cache[b.key] = a
	if r := s.refused[b.key]; r != nil {
		r.standing = false
	}
	if b.proves && len(s.standingLocked(b.id)) == 0 {
		s.setLocked(b.id, Valid, "")
		// A named identity that works makes its provider's bare key stale.
		if b.id.Name != "" {
			s.removeLocked(Key{Provider: b.id.Provider})
		}
	}
	return a, nil
}

// expiresAt is when the cache lets a credential go: a minute before the expiry the
// tool reported, or ttl from now when it reported none.
func (s *Store) expiresAt(reported time.Time, ttl time.Duration) time.Time {
	if reported.IsZero() {
		return s.opt.now().Add(ttl)
	}
	return reported.Add(-expiryMargin)
}

// cli is one provider's tool: its name in a message, and the lines on its stderr
// that mean its login has expired.
type cli struct {
	provider Provider
	name     string
	expired  []*regexp.Regexp
}

// phrases matches each text anywhere in a line.
func phrases(texts ...string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(texts))
	for i, t := range texts {
		res[i] = regexp.MustCompile(regexp.QuoteMeta(t))
	}
	return res
}

// codes matches each code as a whole word, so AADSTS70008 is not AADSTS700084.
func codes(texts ...string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(texts))
	for i, t := range texts {
		res[i] = regexp.MustCompile(`\b` + regexp.QuoteMeta(t) + `\b`)
	}
	return res
}

// expiry is a tool's exit that said its login has expired; detail is the line
// that said so, rendered through safe.
type expiry struct{ detail string }

func (e *expiry) Error() string { return "credentials: expired: " + e.detail }

// exitError is a tool's non-zero exit. It names the tool and the code and never
// the output, which can hold what the tool was asked for.
type exitError struct {
	tool string
	code int
}

func (e *exitError) Error() string { return fmt.Sprintf("credentials: %s exited %d", e.tool, e.code) }

// tool runs one command of c's tool with env, nil for the sidecar's own, and
// answers its stdout. A non-zero exit is an expiry when stderr holds one of c's
// lines, else an exitError.
func (s *Store) tool(ctx context.Context, c cli, env []string, args ...string) ([]byte, error) {
	stdout, stderr, exit, err := s.run(ctx, env, s.bins[c.provider], args...)
	if err != nil {
		return nil, fmt.Errorf("credentials: %s did not finish: %w", c.name, err)
	}
	if exit != 0 {
		if line, ok := c.expiryLine(stderr); ok {
			return nil, &expiry{detail: safe.String(line)}
		}
		return nil, &exitError{tool: c.name, code: exit}
	}
	return stdout, nil
}

func (c cli) expiryLine(stderr []byte) (string, bool) {
	for line := range strings.Lines(string(stderr)) {
		for _, re := range c.expired {
			if re.MatchString(line) {
				return strings.TrimSpace(line), true
			}
		}
	}
	return "", false
}
