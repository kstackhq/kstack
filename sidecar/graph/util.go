package graph

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"

	gqlerrors "github.com/kstackhq/kstack/sidecar/graph/errors"
	"github.com/kstackhq/kstack/sidecar/graph/model"

	"github.com/kstackhq/kstack/sidecar/internal/chatsvc"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/memorysvc"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
)

// mapStream maps a latest-value source onto the returned channel until ctx ends or sub
// closes. No separate snapshot: the source is current-on-subscribe, so its first value IS
// the snapshot. unsub runs once on teardown, where out is also closed.
func mapStream[S, G any](
	ctx context.Context,
	sub <-chan S,
	unsub func(),
	mapFn func(S) G,
) <-chan G {
	out := make(chan G)
	go func() {
		defer close(out)
		defer unsub()

		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-sub:
				if !ok {
					return
				}
				select {
				case out <- mapFn(s):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// ptrSlice maps a value slice onto pointers into it: the services hand out []T while
// gqlgen's bindings want []*T.
func ptrSlice[T any](items []T) []*T {
	out := make([]*T, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}

// ptrStream is ptrSlice for streams, which nearly every cluster subscription resolver
// needs. The source is current-on-subscribe, so there's nothing to unsubscribe.
func ptrStream[T any](ctx context.Context, sub <-chan T) <-chan *T {
	return mapStream(ctx, sub, func() {}, func(v T) *T { return &v })
}

// nullTime unwraps the sql.NullTime a record holds so a watch can diff it with ==;
// the wire wants a pointer.
func nullTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

// chatRefusals maps the service's named errors onto the wire's coded ones. The
// composer branches on why a send was refused and a Go error's identity does not
// cross GraphQL, so the code is what carries it. Anything absent stays opaque —
// a store failure is not something a client can act on.
var chatRefusals = []struct {
	err  error
	wire *gqlerror.Error
}{
	{chatsvc.ErrBadRequest, gqlerrors.ErrValidationError},
	{chatsvc.ErrChatGone, gqlerrors.ErrRecordNotFound},
	{chatsvc.ErrClusterGone, gqlerrors.ErrRecordNotFound},
	{chatsvc.ErrTurnInFlight, gqlerrors.ErrConflict},
	{chatsvc.ErrStopping, gqlerrors.ErrServiceUnavailable},
	// A chat the model cannot read whole is the model's limit, not the chat's
	// state: the composer names it so the user can pick another model.
	{chatsvc.ErrChatContextFull, gqlerrors.ErrChatContextFull},
	// The sender saw the other switch, so the composer says it changed rather than
	// running the turn where the user did not look.
	{chatsvc.ErrChatSandboxChanged, gqlerrors.ErrChatSandboxChanged},
}

// clusterRefusals maps the cluster service's named errors onto wire codes: an id
// naming nothing is not found; a record its source still declares, or one that
// will not be connected, disagrees with the record's own state, the same shape as a
// turn already in flight.
var clusterRefusals = []struct {
	err  error
	wire *gqlerror.Error
}{
	{clustersvc.ErrNotFound, gqlerrors.ErrRecordNotFound},
	{clustersvc.ErrDeclaredBySource, gqlerrors.ErrConflict},
	{clustersvc.ErrNotConnectable, gqlerrors.ErrConflict},
}

// clusterErr is what every cluster mutation returns an error through.
func clusterErr(err error) error {
	for _, r := range clusterRefusals {
		if errors.Is(err, r.err) {
			return gqlerrors.Clone(r.wire)
		}
	}
	return err
}

// memoryRefusals maps the memory service's named errors onto wire codes. The
// dialog keeps the user's draft on each and says why.
var memoryRefusals = []struct {
	err  error
	wire *gqlerror.Error
}{
	{memorysvc.ErrBadInput, gqlerrors.ErrValidationError},
	{memorysvc.ErrNotFound, gqlerrors.ErrRecordNotFound},
	{memorysvc.ErrClusterGone, gqlerrors.ErrRecordNotFound},
	{memorysvc.ErrNameTaken, gqlerrors.ErrMemoryNameTaken},
	{memorysvc.ErrFull, gqlerrors.ErrMemoryFull},
	{memorysvc.ErrSecret, gqlerrors.ErrMemorySecret},
	{memorysvc.ErrStopping, gqlerrors.ErrServiceUnavailable},
}

// memoryErr is what every memory resolver returns an error through.
func memoryErr(err error) error {
	for _, r := range memoryRefusals {
		if errors.Is(err, r.err) {
			return gqlerrors.Clone(r.wire)
		}
	}
	return err
}

// chatErr is what every chat resolver returns an error through.
func chatErr(err error) error {
	for _, r := range chatRefusals {
		if errors.Is(err, r.err) {
			return gqlerrors.Clone(r.wire)
		}
	}
	return err
}

// sandboxPathErr is what every sandbox path resolver returns an error
// through: a refusal in the user's words is a validation error carrying them,
// and anything else stays opaque.
func sandboxPathErr(err error) error {
	var refusal securityconfig.PathRefusal
	if errors.As(err, &refusal) {
		return gqlerrors.NewValidationError("sandbox-path", refusal.Error())
	}
	return err
}

// The path entries' states and sources, by their wire spelling.
var (
	sandboxPathStates = map[securityconfig.PathState]model.SandboxPathState{
		securityconfig.PathAdopted: model.SandboxPathStateAdopted,
		securityconfig.PathPending: model.SandboxPathStatePending,
		securityconfig.PathGone:    model.SandboxPathStateGone,
	}
	sandboxPathSources = map[securityconfig.Source]model.SandboxPathSource{
		securityconfig.SourceShell: model.SandboxPathSourceShell,
		securityconfig.SourceUser:  model.SandboxPathSourceUser,
	}
)

// sandboxPathOf is entries on the wire, in order.
func sandboxPathOf(entries []securityconfig.PathEntry) []*model.SandboxPathEntry {
	out := make([]*model.SandboxPathEntry, len(entries))
	for i, e := range entries {
		out[i] = &model.SandboxPathEntry{
			Dir: e.Dir, Target: e.Target, State: sandboxPathStates[e.State], Source: sandboxPathSources[e.Source], Shared: e.Shared,
		}
	}
	return out
}
