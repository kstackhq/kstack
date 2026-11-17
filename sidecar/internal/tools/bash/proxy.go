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
	"errors"
	"net"
	"net/http"

	"github.com/kstackhq/kstack/sidecar/internal/run/kubeproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A run's grant takes 20 requests a second past a burst of 50, and holds at
// most 32 open at once.
const (
	proxyQPS         = 20
	proxyBurst       = 50
	proxyMaxInFlight = 32
)

// runProxy is a sandboxed run's door: one listener and one server on the
// run's socket, in front of the cluster's grant and each cloud platform's.
type runProxy struct {
	grant  *kubeproxy.Grant // the cluster's; nil for a run with no cluster
	mounts []CloudMount     // each cloud platform's, in the order mounted
	ln     net.Listener
	srv    *http.Server
}

// What a run's grant answers a write with when nobody can be asked: a
// foreground call's with no asker in its runtime, and every background
// command's, whose answer has settled by the time it writes.
const (
	refusedNoAsker    = "kstack: this sandbox reads the cluster and changes nothing."
	refusedBackground = "kstack: a background command cannot change the cluster. Run it in the foreground."
)

// writesFor is what a run's grant does with a write: the asker to put it to,
// or, with none, the refusal to answer it with. A foreground call asks through
// its runtime's ActionAsker; a background command asks no one.
func writesFor(rt tools.Runtime, background bool) (kubeproxy.Asker, string) {
	switch {
	case background:
		return nil, refusedBackground
	case rt.ActionAsker == nil:
		return nil, refusedNoAsker
	}
	return runtimeAsker{rt.ActionAsker}, ""
}

// runtimeAsker is a runtime's ActionAsker as a grant's Asker, so neither
// package imports the other.
type runtimeAsker struct{ a tools.ActionAsker }

func (a runtimeAsker) Ask(ctx context.Context, r kubeproxy.Request) (kubeproxy.Answer, error) {
	answer, err := a.a.Ask(ctx, requestOf(r))
	return kubeproxy.Answer{Approved: answer.Approved, Duration: answer.Duration}, err
}

func (a runtimeAsker) Record(ctx context.Context, r kubeproxy.Request, d permissions.Decision, reason string) error {
	return a.a.Record(ctx, requestOf(r), d, reason)
}

// requestOf is a grant's request as the runtime's asker takes it.
func requestOf(r kubeproxy.Request) tools.ActionRequest {
	out := tools.ActionRequest{
		Action: r.Action, Grantable: r.Grantable, CommandRule: r.CommandRule, ChatRule: r.ChatRule,
		Diff: r.Diff, DiffCut: r.DiffCut, DiffError: r.DiffError,
	}
	if r.Write != nil {
		w := writeOf(*r.Write)
		out.Write = &w
	}
	return out
}

// writeOf is a grant's write as the runtime's asker takes it.
func writeOf(w kubeproxy.Write) tools.ClusterWrite {
	return tools.ClusterWrite{
		Method: w.Method, Path: w.Path, Subresource: w.Subresource,
		ContentType: w.ContentType, Body: string(w.Body), DryRun: w.DryRun,
	}
}

// newClusterGrant is a grant over up for the run of sess in kubeContext,
// whose writes go to asker or are refused with refusal.
func newClusterGrant(up kubeproxy.Upstream, sess session.Session, kubeContext string, asker kubeproxy.Asker, refusal string) *kubeproxy.Grant {
	return kubeproxy.NewGrant(up, sess, kubeContext, asker, refusal, proxyQPS, proxyBurst, proxyMaxInFlight)
}

// serve listens on socket and serves the door over the grants p holds, each
// cloud platform's route reading on.
func (p *runProxy) serve(socket string, on func(permissions.Target) bool) error {
	var cluster http.Handler
	if p.grant != nil {
		cluster = p.grant
	}
	d, err := newDoor(cluster, p.mounts, on)
	if err != nil {
		return err
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	p.ln, p.srv = ln, runproxy.NewServer(d)
	go func() { _ = p.srv.Serve(ln) }()
	return nil
}

// grant is what a run's door ends and joins: the cluster's grant or a
// cloud platform's.
type grant interface {
	End()
	Wait()
}

// grants is every grant p holds, the cluster's first.
func (p *runProxy) grants() []grant {
	var gs []grant
	if p.grant != nil {
		gs = append(gs, p.grant)
	}
	for _, m := range p.mounts {
		gs = append(gs, m.Grant)
	}
	return gs
}

// end ends every grant, which cancels every request in flight, closes the
// server and its socket, then waits for every request's handler to return. A
// handler reading a body returns only once its connection closes, so the close
// comes first; the wait means no request is put to the user once the run is
// over.
func (p *runProxy) end() {
	gs := p.grants()
	for _, g := range gs {
		g.End()
	}
	if p.srv != nil {
		_ = p.srv.Close()
		// Serve may not have taken the listener yet, and Close closes only one
		// it has.
		_ = p.ln.Close()
	}
	for _, g := range gs {
		g.Wait()
	}
}

// door routes each request of a run by its Host to that route's handler, and
// answers anything else as the cluster's grant answers a request it does not
// hold: an unknown host, a CONNECT, a cloud platform whose switch is off. It
// reads nothing else of the request and authenticates nothing: each grant
// checks its own token, so no route is trusted because of its Host.
type door struct {
	routes map[string]doorRoute
	on     func(permissions.Target) bool // a cloud platform's switch, now
}

// doorRoute is one host's handler, and whose it is.
type doorRoute struct {
	handler http.Handler
	target  permissions.Target
}

// newDoor is the door over cluster, nil for a run with none, and mounts, each
// cloud platform's route reading on. No two routes share a Host, and none takes
// the cluster's.
func newDoor(cluster http.Handler, mounts []CloudMount, on func(permissions.Target) bool) (*door, error) {
	d := &door{routes: map[string]doorRoute{}, on: on}
	if cluster != nil {
		d.routes[kubeproxy.Host] = doorRoute{handler: cluster, target: permissions.Cluster}
	}
	for _, m := range mounts {
		for _, r := range m.Routes {
			if _, taken := d.routes[r.Host]; taken || r.Host == kubeproxy.Host {
				return nil, errors.New("two routes on the run's door name the host " + r.Host)
			}
			d.routes[r.Host] = doorRoute{handler: r.Handler, target: m.Grant.Target()}
		}
	}
	return d, nil
}

// ServeHTTP hands r to its route. A cloud platform's route reads its switch on
// every request, so one turned off while the run lives answers the door's 401
// to the next; a request already past the door ends as it would have.
func (d *door) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := d.routes[r.Host]
	if !ok || r.Method == http.MethodConnect || route.target != permissions.Cluster && !d.on(route.target) {
		kubeproxy.Unauthorized(w)
		return
	}
	route.handler.ServeHTTP(w, r)
}
