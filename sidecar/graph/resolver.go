package graph

//go:generate go run github.com/99designs/gqlgen generate

// Hand-written; gqlgen never regenerates this file. Add resolver dependencies here.

import (
	"github.com/kstackhq/kstack/sidecar/internal/auth"
	"github.com/kstackhq/kstack/sidecar/internal/chatsvc"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/memorysvc"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
)

// Resolver carries every operation's dependencies. Each field MUST be non-nil — the
// composition root always wires them and the resolvers call them without guards;
// degraded behavior lives inside the services.
type Resolver struct {
	// ClusterSvc is the boundary to the cluster backend, hiding beehive behind the
	// cluster types. (Named ClusterSvc to avoid shadowing the generated
	// queryResolver.Clusters method.)
	ClusterSvc clustersvc.Service
	// ChatSvc backs the chat mutations and watches. (Named ChatSvc, like ClusterSvc,
	// to stay clear of the generated resolver methods named after the schema's types.)
	ChatSvc chatsvc.Service
	// MemorySvc backs the memory dialog's watch and writes.
	MemorySvc memorysvc.Service
	// LLMSvc answers the models query, every provider's catalog in picker order, and
	// labels a message's provider.
	LLMSvc *llm.Service
	// SandboxStatus answers the sandbox query: whether this machine offers
	// sandboxed Bash.
	SandboxStatus sandbox.Status
	// Auth backs the authState query/watch and the login/logout mutations; it degrades
	// internally when no cloud account is configured.
	Auth auth.Service
	// SecurityCfg is the security settings file.
	SecurityCfg *securityconfig.Store
}
