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
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// reply is what a fake tool answers for one command line. A reply with a hold
// waits until the hold closes or the run's context ends.
type reply struct {
	stdout, stderr string
	exit           int
	hold           chan struct{}
}

// fakeTools is the runner seam: it answers each command line with its staged
// reply, records every one it was asked and the environment it last ran with, and
// fires ran for each. Every AWS borrow reads the credential's account and the
// profile's region, so both are answered unless a test stages its own.
type fakeTools struct {
	mu      sync.Mutex
	replies map[string]reply
	calls   []string
	envs    map[string][]string
	ran     *testutil.Probe[string]
}

func newFakeTools() *fakeTools {
	f := &fakeTools{replies: map[string]reply{}, envs: map[string][]string{}, ran: testutil.NewProbe[string](64)}
	f.on(awsCaller, reply{stdout: `{"UserId":"AROA:dev","Account":"111111111111","Arn":"arn:aws:sts::111111111111:assumed-role/dev/dev"}`})
	f.on(awsRegion, reply{exit: 1})
	return f
}

// on stages the reply to a command line, spelled as the tool's name and its
// arguments joined by spaces.
func (f *fakeTools) on(line string, r reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[line] = r
}

func (f *fakeTools) run(ctx context.Context, env []string, bin string, args ...string) ([]byte, []byte, int, error) {
	line := strings.Join(append([]string{filepath.Base(bin)}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.envs[line] = env
	r, ok := f.replies[line]
	f.mu.Unlock()
	f.ran.Fire(line)
	if !ok {
		return nil, []byte("unknown command"), 2, nil
	}
	if r.hold != nil {
		select {
		case <-r.hold:
		case <-ctx.Done():
			return nil, nil, -1, ctx.Err()
		}
	}
	return []byte(r.stdout), []byte(r.stderr), r.exit, nil
}

// count is how many times a command line ran.
func (f *fakeTools) count(line string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == line {
			n++
		}
	}
	return n
}

// env is the environment a command line last ran with, nil for the sidecar's own.
func (f *fakeTools) env(line string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.envs[line]
}

func (f *fakeTools) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// clock is a test's time, moved by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// every tool, named by its base name, which is what fakeTools keys on.
var allBinaries = Binaries{AWS: "/bin/aws", GH: "/bin/gh", Gcloud: "/bin/gcloud", Az: "/bin/az"}

// newTestStore builds a store over the fake tools and the clock, closed and its
// secrets forgotten when the test ends.
func newTestStore(t *testing.T, f *fakeTools, c *clock, opts ...option) *Store {
	t.Helper()
	t.Cleanup(safe.ResetSecrets)
	opts = append([]option{withRunner(f.run), withClock(c.Now)}, opts...)
	s := newStoreWithOptions(allBinaries, "/home/user", nil, nil, opts...)
	t.Cleanup(s.Close)
	return s
}

// The command lines and answers the tests stage.
const (
	awsExport = "aws configure export-credentials --profile dev --format process"
	awsCaller = "aws sts get-caller-identity --output json"
	awsRegion = "aws configure get region --profile dev"
	ghToken   = "gh auth token --hostname github.com"
	gcloudTok = "gcloud config config-helper --min-expiry=15m --format=json"
	azARM     = "az account get-access-token --resource https://management.azure.com/ --output json"
	azGraph   = "az account get-access-token --resource https://graph.microsoft.com/ --output json"

	secretKey = "secret-access-key-0123456789"
)

// awsJSON is export-credentials' answer with the secret key and expiry given.
func awsJSON(secret, expiration string) string {
	s := `{"Version":1,"AccessKeyId":"ASIAEXAMPLEKEY000001","SecretAccessKey":"` + secret + `"`
	if expiration != "" {
		s += `,"SessionToken":"session-token-0123456789","Expiration":"` + expiration + `"`
	}
	return s + "}"
}

// states is the whole status table.
func (s *Store) states() []State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statesLocked()
}
