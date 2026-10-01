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
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Found is what Discover read, never a secret.
type Found struct {
	Tools    []Tool // one per Provider, in the order of the constants
	Contexts []string
}

// Tool is one provider's tool as Discover found it.
type Tool struct {
	Provider   Provider
	Installed  bool // its binary is named
	Present    bool // installed and signed in
	Identities []Identity
}

// Identity is one identity a tool holds. Label is what a row shows; the other
// fields are the provider's own, empty where they do not apply.
type Identity struct {
	Key                          Key
	Label                        string // "@alice", "dev", "alice@example.com · my-project"
	User                         string // GitHub, Google and Azure
	Project                      string // Google
	Subscription, SubscriptionID string // Azure
	Tenant                       string // Azure
}

// discoverTimeout bounds each tool's look.
const discoverTimeout = 15 * time.Second

// proof is what one tool's look proved about its logins.
type proof int

const (
	nothing   proof = iota // a failure, which proves nothing
	signedIn               // identities found, not known to work
	checked                // gh checked its token with GitHub
	loggedOut              // not installed, or signed into nothing
)

// look is one tool as Discover found it, and what that proved.
type look struct {
	tool  Tool
	proof proof
}

// Discover asks every tool at once what it holds, reading identities and never a
// secret, and writes to the status table only what the look proved.
func (s *Store) Discover(ctx context.Context) Found {
	s.mu.Lock()
	began := s.expiries
	s.mu.Unlock()
	looks := make([]look, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Go(func() {
			// Descends from the store's context, so Close stops it too.
			lookCtx, cancel := context.WithTimeout(s.ctx, discoverTimeout)
			defer cancel()
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			looks[i] = s.look(lookCtx, p)
		})
	}
	wg.Wait()
	var contexts []string
	if s.contexts != nil {
		contexts = s.contexts()
	}

	s.mu.Lock()
	found := Found{Contexts: contexts}
	for _, l := range looks {
		if l.proof == nothing {
			l.tool = s.lastToolLocked(l.tool)
		}
		s.applyLocked(l, began)
		found.Tools = append(found.Tools, l.tool)
	}
	s.found = found
	s.mu.Unlock()
	s.foundOnce.Do(func() { close(s.foundDone) })
	return found
}

// Found is what the last Discover read, the zero value before the first.
func (s *Store) Found() Found {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.found
}

// WaitFound answers Found once the first Discover has ended, or ctx's error.
func (s *Store) WaitFound(ctx context.Context) (Found, error) {
	select {
	case <-s.foundDone:
		return s.Found(), nil
	case <-ctx.Done():
		return Found{}, ctx.Err()
	}
}

// lastToolLocked is a tool's entry in the last Found, since a look that proved
// nothing says nothing new; on the first Discover, one signed into nothing.
func (s *Store) lastToolLocked(t Tool) Tool {
	for _, last := range s.found.Tools {
		if last.Provider == t.Provider {
			return last
		}
	}
	return Tool{Provider: t.Provider, Installed: t.Installed}
}

// applyLocked writes what a look proved. A status a borrow wrote is overwritten
// only by a look that proved it wrong, and an Excluded one never. began is the
// expiry count when the looks began: an expiry past it came after the proof.
func (s *Store) applyLocked(l look, began uint64) {
	p := l.tool.Provider
	bare := Key{Provider: p}
	switch l.proof {
	case nothing:
		return
	case loggedOut:
		for k, st := range s.status {
			if k.Provider == p && st.Status != Excluded {
				s.removeLocked(k)
			}
		}
		if s.status[bare].Status != Excluded {
			s.setLocked(bare, Missing, "")
		}
		return
	}
	listed := map[Key]bool{}
	for _, id := range l.tool.Identities {
		listed[id.Key] = true
	}
	for k, st := range s.status {
		if k.Provider == p && !listed[k] && st.Status != Excluded {
			s.removeLocked(k)
		}
	}
	for k := range listed {
		st, ok := s.status[k]
		switch {
		case st.Status == Excluded:
		case l.proof == checked && s.expiredAt[k] <= began:
			// The tool has just shown its login works, so nothing it answered stays
			// refused or expired.
			s.setLocked(k, Valid, "")
			for ck, b := range s.borrowings {
				if b.id == k {
					delete(s.refused, ck)
					delete(s.backoff, ck)
				}
			}
		case !ok || st.Status == Missing:
			s.setLocked(k, Valid, "")
		}
	}
}

// look runs one provider's discovery.
func (s *Store) look(ctx context.Context, p Provider) look {
	if s.bins[p] == "" {
		return look{tool: Tool{Provider: p}, proof: loggedOut}
	}
	switch p {
	case AWS:
		return s.lookAWS(ctx)
	case GitHub:
		return s.lookGitHub(ctx)
	case Google:
		return s.lookGoogle(ctx)
	default:
		return s.lookAzure(ctx)
	}
}

// ask runs one discovery command. A run that did not finish is exit -1.
func (s *Store) ask(ctx context.Context, p Provider, args ...string) (stdout, stderr string, exit int) {
	out, errOut, exit, err := s.run(ctx, nil, s.bins[p], args...)
	if err != nil {
		return "", "", -1
	}
	return string(out), string(errOut), exit
}

// proved is a tool found installed and what its look proved, logging its exit
// alone when that is nothing.
func proved(c cli, exit int, pr proof, ids ...Identity) look {
	if pr == nothing {
		slog.Info("credentials: a tool's discovery failed", "tool", c.name, "exit", exit)
	}
	return look{tool: Tool{Provider: c.provider, Installed: true, Present: len(ids) > 0, Identities: ids}, proof: pr}
}

func (s *Store) lookAWS(ctx context.Context) look {
	out, _, exit := s.ask(ctx, AWS, "configure", "list-profiles")
	if exit != 0 {
		return proved(awsCLI, exit, nothing)
	}
	var ids []Identity
	for line := range strings.Lines(out) {
		if name := strings.TrimSpace(line); name != "" {
			ids = append(ids, Identity{Key: Key{AWS, name}, Label: name})
		}
	}
	if len(ids) == 0 {
		return proved(awsCLI, exit, loggedOut)
	}
	return proved(awsCLI, exit, signedIn, ids...)
}

var (
	// gh 2.40 on: one line per account on a host, each followed by whether it is
	// the active one.
	ghAccountRE = regexp.MustCompile(`Logged in to \S+ account (\S+)`)
	// Before 2.40: one account per host.
	ghAsRE = regexp.MustCompile(`Logged in to \S+ as (\S+)`)
)

func (s *Store) lookGitHub(ctx context.Context) look {
	out, errOut, exit := s.ask(ctx, GitHub, "auth", "status", "--hostname", "github.com")
	text := out + "\n" + errOut
	switch {
	case exit == 0:
		user := ghUser(text)
		label := "github.com"
		if user != "" {
			label = "@" + user
		}
		return proved(githubCLI, exit, checked, Identity{Key: Key{Provider: GitHub}, Label: label, User: user})
	case exit > 0 && ghLoggedOut(text):
		return proved(githubCLI, exit, loggedOut)
	default:
		return proved(githubCLI, exit, nothing)
	}
}

// ghLoggedOut is whether gh auth status said github.com holds no login: no host
// at all, or other hosts alone — gh 2.40 on, then before it.
func ghLoggedOut(text string) bool {
	return strings.Contains(text, "You are not logged into any GitHub hosts") ||
		strings.Contains(text, "You are not logged into any accounts on github.com") ||
		strings.Contains(text, `"github.com" not found among authenticated GitHub hosts`)
}

// ghUser is the active account gh auth status names, "" when it names none.
func ghUser(text string) string {
	var account string
	for line := range strings.Lines(text) {
		if m := ghAccountRE.FindStringSubmatch(line); m != nil {
			account = m[1]
		} else if strings.Contains(line, "Active account: true") {
			return account
		} else if m := ghAsRE.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

func (s *Store) lookGoogle(ctx context.Context) look {
	out, _, exit := s.ask(ctx, Google, "config", "get-value", "account")
	if exit != 0 {
		return proved(googleCLI, exit, nothing)
	}
	// An unset value prints nothing on stdout and "(unset)" on stderr.
	account := strings.TrimSpace(out)
	if account == "" || account == "(unset)" {
		return proved(googleCLI, exit, loggedOut)
	}
	id := Identity{Key: Key{Provider: Google}, Label: account, User: account}
	if out, _, exit := s.ask(ctx, Google, "config", "get-value", "project"); exit == 0 {
		if project := strings.TrimSpace(out); project != "" && project != "(unset)" {
			id.Project = project
			id.Label = account + " · " + project
		}
	}
	return proved(googleCLI, exit, signedIn, id)
}

func (s *Store) lookAzure(ctx context.Context) look {
	out, errOut, exit := s.ask(ctx, Azure, "account", "show", "--output", "json")
	if exit > 0 && strings.Contains(errOut, "Please run 'az login'") {
		return proved(azureCLI, exit, loggedOut)
	}
	var account struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		TenantID string `json:"tenantId"`
		User     struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if exit != 0 || json.Unmarshal([]byte(out), &account) != nil {
		return proved(azureCLI, exit, nothing)
	}
	return proved(azureCLI, exit, signedIn, Identity{
		Key:            Key{Provider: Azure},
		Label:          account.User.Name + " · " + account.Name,
		User:           account.User.Name,
		Subscription:   account.Name,
		SubscriptionID: account.ID,
		Tenant:         account.TenantID,
	})
}
