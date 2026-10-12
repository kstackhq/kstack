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

package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// ErrRevoked is a cluster Kstack does not connect to: disabled, marked for
// deletion, or gone, before the read or under it.
var ErrRevoked = &Refusal{"Kstack no longer connects to this cluster"}

// maxStatusBody is how much of a refusal's body is read for its message.
const maxStatusBody = 4 << 10

// statusError is a request the API server refused: its HTTP code and the message
// its Status carried, cleaned as a line is.
type statusError struct {
	code    int
	message string
}

func (e *statusError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("the API server answered %d", e.code)
	}
	return e.message
}

// errRetired is a read the connection's retirement cut short: the pool rebuilt it,
// so a read opened again goes over its successor.
var errRetired = errors.New("logs: the connection was replaced under the read")

// claim is one cluster's connection, held for a fetch or a follow: the lease, the
// server UID the mirror's pods came from, and for a follow the record's watch, which
// revokes the claim once Kstack no longer connects to the cluster.
type claim struct {
	lease     cluster.Lease
	serverUID string
	// revoked is nil for a claim nothing watches, which is never revoked.
	revoked <-chan struct{}
	stop    context.CancelFunc
}

// acquireClaim claims cluster id's connection for the server serverUID, checking
// the record once. It does not dial. A fetch holds it for one page, which the
// connection's own retirement already bounds, so nothing watches the record.
func acquireClaim(ctx context.Context, svc cluster.Service, id cluster.ClusterID, serverUID string) (*claim, error) {
	lease, err := svc.AcquireConnection(ctx, id)
	switch {
	case errors.Is(err, cluster.ErrNotConnectable), errors.Is(err, cluster.ErrNotFound):
		return nil, ErrRevoked
	case err != nil:
		return nil, err
	}
	return &claim{lease: lease, serverUID: serverUID, stop: func() {}}, nil
}

// openClaim is acquireClaim watching the record until close, opening the watch
// again retry after one that ends, for a follow that holds its claim for as long as
// the user looks.
func openClaim(ctx context.Context, svc cluster.Service, id cluster.ClusterID, serverUID string, retry time.Duration) (*claim, error) {
	c, err := acquireClaim(ctx, svc, id, serverUID)
	if err != nil {
		return nil, err
	}
	watchCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := svc.Clusters().Watch(watchCtx, id)
	if err != nil {
		stop()
		c.lease.Release()
		return nil, err
	}
	revoked := make(chan struct{})
	go func() {
		if watchRevoked(watchCtx, svc, id, stream, retry) {
			close(revoked)
		}
	}()
	c.revoked, c.stop = revoked, stop
	return c, nil
}

// watchRevoked is whether the record's watch showed the cluster Kstack no longer
// connects to, watching until it does or ctx ends. A watch that ends is opened
// again after retry, its snapshot saying whether the record still stands; one
// that cannot be opened again, or ends before its snapshot closes, revokes, since
// a claim nothing watches could outlive the cluster.
func watchRevoked(ctx context.Context, svc cluster.Service, id cluster.ClusterID, stream *cluster.Stream[cluster.ClusterWatchFrame], retry time.Duration) bool {
	for {
		seen, synced := false, false
		for f := range stream.Frames {
			switch {
			case f.Type == cluster.DeltaFrameDeleted || f.Cluster != nil && !connectable(f.Cluster):
				return true
			case f.Cluster != nil:
				seen = true
			case f.Type == cluster.DeltaFrameBookmark:
				if !seen {
					return true
				}
				synced = true
			}
		}
		if ctx.Err() != nil {
			return false
		}
		if !synced {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(retry):
		}
		var err error
		if stream, err = svc.Clusters().Watch(ctx, id); err != nil {
			return ctx.Err() == nil
		}
	}
}

// connectable is whether AcquireConnection would claim c: enabled, not marked for
// deletion, and from the kubeconfig.
func connectable(c *cluster.Cluster) bool {
	return c.Spec.Enabled && c.DeletionRequestedAt == nil && c.KubeContext() != ""
}

// get is one GET of path with query on the claimed server, through the
// connection's own client, so the user's credentials authenticate it. The body is
// read as a stream: it ends when ctx does, when the claim is revoked (ErrRevoked),
// or when the connection is retired (errRetired). A status other than 200 is a
// *statusError.
func (c *claim) get(ctx context.Context, path string, query url.Values) (io.ReadCloser, error) {
	if c.isRevoked() {
		return nil, ErrRevoked
	}
	conn, err := c.lease.ConnFor(ctx, c.serverUID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-c.revoked:
			cancel(ErrRevoked)
		case <-conn.Done():
			cancel(errRetired)
		case <-ctx.Done():
		}
	}()
	u := conn.BaseURL.JoinPath(path)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		cancel(nil)
		return nil, err
	}
	resp, err := conn.HTTPClient.Do(req)
	if err != nil {
		err = endedBy(ctx, err)
		cancel(nil)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer cancel(nil)
		defer resp.Body.Close()
		return nil, statusOf(resp)
	}
	return body{resp.Body, ctx, cancel}, nil
}

// endedBy is err, or the claim's reason when the claim ended the request: the
// transport reports either as a plain cancel, which would read as the caller's.
func endedBy(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, ErrRevoked) || errors.Is(cause, errRetired) {
		return cause
	}
	return err
}

// isRevoked is whether the claim has been revoked.
func (c *claim) isRevoked() bool {
	select {
	case <-c.revoked:
		return true
	default:
		return false
	}
}

// statusOf is a refusal's code and the message of the Status it carries,
// redacted as every diagnostic is.
func statusOf(resp *http.Response) *statusError {
	var status struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxStatusBody)).Decode(&status) // a body that is no Status has no message
	return &statusError{code: resp.StatusCode, message: stripControl(safe.String(status.Message))}
}

// body is a response's body that ends its request's context once closed, and
// names the claim's reason when the claim cut it.
type body struct {
	io.ReadCloser
	ctx    context.Context
	cancel context.CancelCauseFunc
}

func (b body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = endedBy(b.ctx, err)
	}
	return n, err
}

func (b body) Close() error {
	b.cancel(nil)
	return b.ReadCloser.Close()
}

// close ends the record's watch and releases the lease.
func (c *claim) close() {
	c.stop()
	c.lease.Release()
}
