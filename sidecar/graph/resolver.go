package graph

//go:generate go run github.com/99designs/gqlgen generate

// Hand-written; gqlgen never regenerates this file. Add resolver dependencies here.

import (
	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/agent/chat"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/auth"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// Resolver carries every operation's dependencies. Each field MUST be non-nil — the
// composition root always wires them and the resolvers call them without guards;
// degraded behavior lives inside the services.
type Resolver struct {
	// ClusterSvc is the boundary to the cluster backend, hiding beehive behind the
	// cluster types. (Named ClusterSvc to avoid shadowing the generated
	// queryResolver.Clusters method.)
	ClusterSvc cluster.Service
	// ChatSvc backs the chat mutations and watches. (Named ChatSvc, like ClusterSvc,
	// to stay clear of the generated resolver methods named after the schema's types.)
	ChatSvc chat.Service
	// MemorySvc backs the memory dialog's watch and writes.
	MemorySvc memory.Service
	// LLMSvc answers the models query, every provider's catalog in picker order, and
	// labels a message's provider.
	LLMSvc *llm.Service
	// SandboxStatus answers the sandbox query: whether this machine offers
	// sandboxed Bash.
	SandboxStatus sandbox.Status
	// Auth backs the authState query/watch and the login/logout mutations; it degrades
	// internally when no cloud account is configured.
	Auth auth.Service
	// Settings is the user's settings file, and the frozen PATH kept in it.
	Settings *settings.Service
	// Executables probes the executables sandboxed commands run: the Bash tool, nil where
	// there is no shell.
	Executables ExecutableProber
}

// ExecutableProber is the executable probe: *bash.Tool, or a test's fake. StartProbe
// and Report take the user's registered executables, the curated ones first, and
// WatchProbe says when a probe starts and ends.
type ExecutableProber interface {
	StartProbe(registered []settings.Executable)
	Report(registered []settings.Executable) []bash.ExecutableReport
	WatchProbe() *watch.Receiver[bash.ProbeState]
}

// probes reports whether this machine probes executables: a sandbox and a shell.
func (r *Resolver) probes() bool {
	return r.SandboxStatus.Available && r.Executables != nil
}

// registeredExecutables is the user's registered executables, as stored.
func (r *Resolver) registeredExecutables() []settings.Executable {
	return r.Settings.Get().Executables
}
