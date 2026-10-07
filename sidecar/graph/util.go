package graph

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"

	gqlerrors "github.com/kstackhq/kstack/sidecar/graph/errors"
	"github.com/kstackhq/kstack/sidecar/graph/model"

	"github.com/kstackhq/kstack/sidecar/internal/agent/chat"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/run/kubeproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
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
	{chat.ErrBadRequest, gqlerrors.ErrValidationError},
	{chat.ErrChatGone, gqlerrors.ErrRecordNotFound},
	{chat.ErrClusterGone, gqlerrors.ErrRecordNotFound},
	{chat.ErrTurnInFlight, gqlerrors.ErrConflict},
	{chat.ErrStopping, gqlerrors.ErrServiceUnavailable},
	// A chat the model cannot read whole is the model's limit, not the chat's
	// state: the composer names it so the user can pick another model.
	{chat.ErrChatContextFull, gqlerrors.ErrChatContextFull},
	// The sender saw the other switch, so the composer says it changed rather than
	// running the turn where the user did not look.
	{chat.ErrChatSandboxChanged, gqlerrors.ErrChatSandboxChanged},
	{chat.ErrGrantGone, gqlerrors.ErrRecordNotFound},
	{chat.ErrChatNetworkChanged, gqlerrors.ErrChatNetworkChanged},
}

// clusterRefusals maps the cluster service's named errors onto wire codes: an id
// naming nothing is not found; a record its source still declares, or one that
// will not be connected, disagrees with the record's own state, the same shape as a
// turn already in flight.
var clusterRefusals = []struct {
	err  error
	wire *gqlerror.Error
}{
	{cluster.ErrNotFound, gqlerrors.ErrRecordNotFound},
	{cluster.ErrDeclaredBySource, gqlerrors.ErrConflict},
	{cluster.ErrNotConnectable, gqlerrors.ErrConflict},
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
	{memory.ErrBadInput, gqlerrors.ErrValidationError},
	{memory.ErrNotFound, gqlerrors.ErrRecordNotFound},
	{memory.ErrClusterGone, gqlerrors.ErrRecordNotFound},
	{memory.ErrNameTaken, gqlerrors.ErrMemoryNameTaken},
	{memory.ErrFull, gqlerrors.ErrMemoryFull},
	{memory.ErrSecret, gqlerrors.ErrMemorySecret},
	{memory.ErrStopping, gqlerrors.ErrServiceUnavailable},
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

// noSandboxExecutables refuses a probe or a register on a machine with no sandbox.
func noSandboxExecutables() error {
	return gqlerrors.NewValidationError("sandbox-executable", securityconfig.ErrNoSandbox.Error())
}

// sandboxExecutableErr is what the executable resolvers return an error through: a
// refusal in the user's words is a validation error carrying them, and
// anything else stays opaque.
func sandboxExecutableErr(err error) error {
	var refusal securityconfig.ExecutableRefusal
	if errors.As(err, &refusal) {
		return gqlerrors.NewValidationError("sandbox-executable", refusal.Error())
	}
	return err
}

// sandboxExecutablesOf is reports on the wire, in order.
func sandboxExecutablesOf(reports []bash.ExecutableReport) []*model.SandboxExecutable {
	out := make([]*model.SandboxExecutable, len(reports))
	for i, r := range reports {
		out[i] = &model.SandboxExecutable{
			Name: r.Name, Invocation: r.Invocation, Registered: r.Registered, Probed: r.Probed,
			Resolved: r.Resolved, Shim: r.Shim, Target: r.Target, Ok: r.OK, Version: r.Version,
			Error: r.Error,
		}
	}
	return out
}

// folderErr is what every folder grant resolver returns an error through: a
// folder that cannot be granted is a validation error naming the check that
// refused it, a link's carrying the path it leads to, so the webview can offer
// that one.
func folderErr(err error) error {
	var refusal securityconfig.FolderRefusal
	var shape securityconfig.Refusal
	switch {
	case errors.As(err, &refusal):
		e := gqlerrors.NewValidationError(refusal.Rule, refusal.Reason)
		if refusal.Target != "" {
			e.Extensions["target"] = refusal.Target
		}
		return e
	case errors.As(err, &shape):
		return gqlerrors.NewValidationError("folder", shape.Error())
	case errors.Is(err, securityconfig.ErrHeld):
		return gqlerrors.NewValidationError("folder", chat.RulesHeldReason)
	}
	return chatErr(err)
}

// sandboxFolders is the folders granted, always and for chatID ("" for no
// chat), with what no grant opens, as Settings and the composer draw them.
func (r *Resolver) sandboxFolders(ctx context.Context, chatID apimeta.ChatID) (*model.SandboxFolders, error) {
	always, chat := r.ChatSvc.FolderGrants(ctx, chatID)
	out := &model.SandboxFolders{
		Always: sandboxFoldersOf(always), Chat: sandboxFoldersOf(chat),
		Never: []string{}, Wide: []string{},
		RulesHeld: r.SecurityCfg.Held(securityconfig.FieldRules),
	}
	if r.SandboxStatus.Available {
		out.Never = append(out.Never, r.SecurityCfg.NeverReadable(ctx)...)
		out.Wide = append(out.Wide, r.SecurityCfg.WideFolders(ctx)...)
	}
	return out, nil
}

// sandboxFoldersOf is grants on the wire, in order.
func sandboxFoldersOf(grants []chat.FolderGrant) []*model.SandboxFolder {
	out := make([]*model.SandboxFolder, len(grants))
	for i, g := range grants {
		out[i] = &model.SandboxFolder{ID: g.ID, Path: g.Path, Write: g.Write}
		if g.Refused != "" {
			out[i].Refused = &g.Refused
		}
	}
	return out
}

// permissionSettings is the permission modes and rules as Settings shows them,
// each known context's mode among them.
func (r *Resolver) permissionSettings(ctx context.Context) (*model.PermissionSettings, error) {
	clusters, err := r.ClusterSvc.Clusters().List(ctx)
	if err != nil {
		return nil, err
	}
	var contexts []string
	for _, c := range clusters {
		if name := c.KubeContext(); name != "" && c.DeletionRequestedAt == nil {
			contexts = append(contexts, name)
		}
	}
	slices.Sort(contexts)
	contexts = slices.Compact(contexts)

	cfg := r.SecurityCfg
	out := &model.PermissionSettings{DefaultMode: cfg.DefaultMode(), Destructive: kubeproxy.Destructive, Held: []string{}}
	for _, c := range contexts {
		st := cfg.ModeFor(c)
		out.Contexts = append(out.Contexts, &st)
	}
	for _, rule := range cfg.Get().Rules {
		out.Rules = append(out.Rules, &rule)
	}
	for _, field := range []string{securityconfig.FieldDefaultMode, securityconfig.FieldModes, securityconfig.FieldRules} {
		if cfg.Held(field) {
			out.Held = append(out.Held, field)
		}
	}
	return out, nil
}

// approvalErr is what approvalDecide returns an error through: an Always
// answer refused while the settings hold rules Kstack cannot read names the
// file, and anything else is a chat refusal.
func approvalErr(err error) error {
	if errors.Is(err, securityconfig.ErrHeld) {
		return gqlerrors.NewValidationError("decision",
			"security.json holds rules Kstack cannot read, so no rule can be added: fix them in Settings, or approve once")
	}
	return chatErr(err)
}

// permissionsAfter is a permission mutation's answer: its refusal, else the
// settings it left.
func (r *Resolver) permissionsAfter(ctx context.Context, err error) (*model.PermissionSettings, error) {
	var refusal securityconfig.Refusal
	switch {
	case errors.As(err, &refusal):
		return nil, gqlerrors.NewValidationError("permission", refusal.Error())
	case errors.Is(err, securityconfig.ErrHeld):
		return nil, gqlerrors.NewValidationError("permission", "the security settings file holds a value Kstack cannot read: fix it, or discard what Kstack cannot read")
	case errors.Is(err, securityconfig.ErrNoRule), errors.Is(err, securityconfig.ErrNotHeld):
		return nil, gqlerrors.NewValidationError("permission", err.Error())
	case err != nil:
		return nil, err
	}
	return r.permissionSettings(ctx)
}

// rulePointers is rules as gqlgen serves a list of them: never null.
func rulePointers(rules []permissions.Rule) []*permissions.Rule {
	out := make([]*permissions.Rule, 0, len(rules))
	for i := range rules {
		out = append(out, &rules[i])
	}
	return out
}
