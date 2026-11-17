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

package bash

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A CloudPlatform is one cloud platform a sandboxed run can reach.
type CloudPlatform interface {
	Target() permissions.Target
	// Mount builds the cloud platform's part of one run: its grant, its routes
	// on the run's door, and the files it writes under the run's directories.
	Mount(ctx context.Context, run CloudRun) (CloudMount, error)
}

// CloudRun is what a cloud platform may read of the run it mounts on, and its
// grant's inputs: the session, and the asker or, with none, the refusal.
type CloudRun struct {
	Port     int    // the loopback port the run dials its door on
	Dir      string // the run's directory, read-only to the run
	ToolHome string
	TmpDir   string // the run's TMPDIR
	// CAFile is where the run's certificate bundle is, once a mount returns a
	// TLS route: the run's certificate, then the system's roots.
	CAFile  string
	Session session.Session
	Asker   runproxy.Asker
	Refusal string
}

// CloudMount is what a cloud platform adds to one run.
type CloudMount struct {
	Routes []Route
	Env    []string        // rows for the run's environment
	Grant  *runproxy.Grant // required: the run ends it
}

// cloudLimits bound each cloud platform's grant; one whose client sends proxy
// credentials only once asked sets Challenges on a copy.
var cloudLimits = runproxy.Limits{QPS: proxyQPS, Burst: proxyBurst, Open: proxyMaxInFlight, Bodies: proxyBodies}

// Route is one Host on the run's door, over one transport, and its handler.
type Route struct {
	Host    string
	TLS     bool // served over the door's TLS, with the run's certificate
	Handler http.Handler
}

// Cloud is the cloud platforms this build has, and each one's switch.
type Cloud struct {
	Platforms []CloudPlatform
	// On reads a cloud platform's switch, at a run's start and on every
	// request; nil reads every one off.
	On func(permissions.Target) bool
}

// on is whether target's switch is on now.
func (c Cloud) on(target permissions.Target) bool {
	return c.On != nil && c.On(target)
}

// platformNames is each cloud platform in the user's words.
var platformNames = map[permissions.Target]string{
	permissions.AWS:    "AWS",
	permissions.GitHub: "GitHub",
	permissions.GCP:    "Google Cloud",
	permissions.Azure:  "Azure",
}

// platformsFor is the cloud platforms a run of rt mounts: those whose switch is
// on at its start, for a run a turn of the user's started, a chat's or a
// subagent's. A monitor's, and the probe's, which names no kind, get none.
func (c Cloud) platformsFor(rt tools.Runtime) []CloudPlatform {
	if rt.Session.Kind != session.Chat && rt.Session.Kind != session.Subagent {
		return nil
	}
	var on []CloudPlatform
	for _, p := range c.Platforms {
		if c.on(p.Target()) {
			on = append(on, p)
		}
	}
	return on
}

// mount adds each cloud platform's part of run to the door, its requests that
// ask going as cloudAskerFor says, and answers the variables the mounts set. It
// keeps the mounts made before one that fails, for end to end.
func (p *runProxy) mount(ctx context.Context, platforms []CloudPlatform, rt tools.Runtime, background bool, run CloudRun) ([]string, error) {
	for _, platform := range platforms {
		run.Asker, run.Refusal = cloudAskerFor(rt, background, platform.Target())
		m, err := platform.Mount(ctx, run)
		if err != nil {
			return nil, err
		}
		if m.Grant == nil {
			return nil, fmt.Errorf("the %s mount names no grant", platformNames[platform.Target()])
		}
		p.mounts = append(p.mounts, m)
	}
	return cloudEnv(p.mounts)
}

// cloudAskerFor is where a cloud platform's grant puts a request that asks: a
// foreground call's runtime asker, or, for a background command and a runtime
// with none, the refusal naming the cloud platform.
func cloudAskerFor(rt tools.Runtime, background bool, target permissions.Target) (runproxy.Asker, string) {
	name := platformNames[target]
	switch {
	case background:
		return nil, "kstack: a background command cannot change " + name + ". Run it in the foreground."
	case rt.ActionAsker == nil:
		return nil, "kstack: this sandbox reads " + name + " and changes nothing."
	}
	return cloudAsker{rt.ActionAsker}, ""
}

// cloudAsker is a runtime's ActionAsker as a cloud platform grant's Asker, so
// neither package imports the other.
type cloudAsker struct{ a tools.ActionAsker }

func (a cloudAsker) Ask(ctx context.Context, r runproxy.Request) (bool, error) {
	answer, err := a.a.Ask(ctx, cloudRequestOf(r))
	return answer.Approved, err
}

func (a cloudAsker) Record(ctx context.Context, r runproxy.Request, d permissions.Decision, reason string) error {
	return a.a.Record(ctx, cloudRequestOf(r), d, reason)
}

// cloudRequestOf is a cloud platform grant's request as the runtime's asker
// takes it.
func cloudRequestOf(r runproxy.Request) tools.ActionRequest {
	var details []tools.Detail
	for _, d := range r.Sent.Details {
		details = append(details, tools.Detail{Label: d.Label, Value: d.Value})
	}
	return tools.ActionRequest{Action: r.Action, Cloud: &tools.CloudRequest{
		Host: r.Sent.Host, Method: r.Sent.Method, Path: r.Sent.Path, ContentType: r.Sent.ContentType,
		Body: string(r.Sent.Body), Unshown: r.Sent.Unshown, Details: details,
	}}
}

// proxyVars are the variables that send another tool's requests through a
// proxy.
var proxyVars = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "all_proxy": true,
}

// cloudEnv is the variables every mount sets, in order, each name once. Two
// mounts setting one name to different values fail the run. A run where any
// sets a proxy variable also holds NO_PROXY, so a client dialling the
// loopback authority is not sent round through it.
func cloudEnv(mounts []CloudMount) ([]string, error) {
	var rows []string
	proxied := false
	for _, m := range mounts {
		for _, kv := range m.Env {
			name, _, _ := strings.Cut(kv, "=")
			proxied = proxied || proxyVars[name]
		}
		rows = append(rows, m.Env...)
	}
	if proxied {
		rows = append(rows, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	}
	var env []string
	values := map[string]string{}
	for _, kv := range rows {
		name, value, _ := strings.Cut(kv, "=")
		switch set, ok := values[name]; {
		case !ok:
			values[name] = value
			env = append(env, kv)
		case set != value:
			return nil, fmt.Errorf("two cloud platforms set %s to different values", name)
		}
	}
	return env, nil
}
