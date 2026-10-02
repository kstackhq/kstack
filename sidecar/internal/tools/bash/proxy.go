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
	"net"
	"net/http"

	"github.com/kstackhq/kstack/sidecar/internal/kubeproxy"
	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// A run's grant takes 20 requests a second past a burst of 50, and holds at
// most 32 open at once.
const (
	proxyQPS         = 20
	proxyBurst       = 50
	proxyMaxInFlight = 32
)

// runProxy is a sandboxed run's way to its chat's cluster: a grant over the
// run's claim, and the server on the run's socket.
type runProxy struct {
	grant *kubeproxy.Grant
	ln    net.Listener
	srv   *http.Server
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
// its runtime's ClusterWriteAsker; a background command asks no one.
func writesFor(rt tools.Runtime, background bool) (kubeproxy.Asker, string) {
	switch {
	case background:
		return nil, refusedBackground
	case rt.ClusterWriteAsker == nil:
		return nil, refusedNoAsker
	}
	return runtimeAsker{rt.ClusterWriteAsker}, ""
}

// runtimeAsker is a runtime's ClusterWriteAsker as a grant's Asker, so
// neither package imports the other.
type runtimeAsker struct{ w tools.ClusterWriteAsker }

func (a runtimeAsker) Ask(ctx context.Context, w kubeproxy.Write) (bool, error) {
	return a.w.Ask(ctx, requestOf(w))
}

func (a runtimeAsker) Record(ctx context.Context, w kubeproxy.Write, d permissions.Decision, why permissions.Reason) error {
	return a.w.Record(ctx, requestOf(w), d, why)
}

// requestOf is a grant's write as the runtime's asker takes it.
func requestOf(w kubeproxy.Write) tools.ClusterWriteRequest {
	return tools.ClusterWriteRequest{
		Method: w.Method, Path: w.Path, Subresource: w.Subresource,
		ContentType: w.ContentType, Body: string(w.Body), DryRun: w.DryRun, Action: &w.Action,
	}
}

// startProxy serves a grant over up for the run of sess in kubeContext on
// socket, whose writes go to asker or are refused with refusal.
func startProxy(up kubeproxy.Upstream, sess session.Session, kubeContext, socket string, asker kubeproxy.Asker, refusal string) (*runProxy, error) {
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	grant := kubeproxy.NewGrant(up, sess, kubeContext, asker, refusal, proxyQPS, proxyBurst, proxyMaxInFlight)
	p := &runProxy{grant: grant, ln: ln, srv: kubeproxy.NewServer(grant)}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// end ends the grant, which cancels every request in flight, closes the server
// and its socket, then waits for every request's handler to return. A handler
// reading a body returns only once its connection closes, so the close comes
// first; the wait means no write is put to the user once the run is over.
func (p *runProxy) end() {
	p.grant.End()
	_ = p.srv.Close()
	// Serve may not have taken the listener yet, and Close closes only one it
	// has.
	_ = p.ln.Close()
	p.grant.Wait()
}
