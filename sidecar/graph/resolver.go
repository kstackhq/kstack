package graph

//go:generate go tool gqlgen generate

// Hand-written; gqlgen never regenerates this file. Add resolver dependencies here.

import (
	"context"

	"github.com/amorey/gochan/watch"

	"github.com/kstackhq/kstack/sidecar/internal/agent/chat"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/providerkeys"
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
	// MemorySvc backs the memory popover's watch and writes.
	MemorySvc memory.Service
	// LLMSvc answers the models query, every provider's catalog in picker order, and
	// labels a message's provider.
	LLMSvc *llm.Service
	// SandboxStatus answers the sandbox query: whether this machine offers
	// sandboxed Bash.
	SandboxStatus sandbox.Status
	// Auth is the Kstack Cloud connector, which backs the authState query and watch and
	// the login and logout mutations: *kstackcloud.Service, or a test's fake.
	Auth AuthConnector
	// Settings is the user's settings file, and the frozen PATH kept in it.
	Settings *settings.Service
	// Executables probes the executables sandboxed commands run: the Bash tool, nil where
	// there is no shell.
	Executables ExecutableProber
	// Logs pages and follows a scope's lines.
	Logs LogsService
	// CloudTargets are the cloud platforms this build has, in the order
	// Settings lists them: the only ones whose switch the wire reads or writes.
	CloudTargets []permissions.Target
	// ChatGPT is the ChatGPT connector: *chatgpt.Service, or a test's fake.
	ChatGPT ChatGPTConnector
	// OpenRouter is the OpenRouter connector: *openrouter.Service, or a
	// test's fake.
	OpenRouter OpenRouterConnector
	// AWS is the AWS connector: *aws.Service, or a test's fake.
	AWS AWSConnector
	// GitHub is the GitHub connector: *github.Service, or a test's fake.
	GitHub GitHubConnector
	// Google is the Google connector: *google.Service, or a test's fake.
	Google GoogleConnector
	// ProviderKeys holds the keyed providers' keys: *providerkeys.Service, or
	// a test's fake.
	ProviderKeys ProviderKeyStore
	// OpenURL opens one of the sidecar's own addresses in the browser:
	// browser.Open, or a test's fake. No caller hands it an address.
	OpenURL func(url string) error
}

// ProviderKeyStore is the keyed providers' keys as the wire drives them. No
// method returns a key.
type ProviderKeyStore interface {
	States() []providerkeys.State
	Subscribe() (<-chan []providerkeys.State, func())
	Save(id, key string) error
	Delete(id string) error
}

// AuthConnector is the Kstack Cloud connector as the wire drives it.
type AuthConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
}

// ChatGPTConnector is the ChatGPT connector as the wire drives it.
type ChatGPTConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context, fresh bool) error
	Cancel()
	Disconnect(ctx context.Context) error
}

// OpenRouterConnector is the OpenRouter connector as the wire drives it.
type OpenRouterConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context) error
	Cancel()
	Disconnect() error
}

// AWSConnector is the AWS connector as the wire drives it.
type AWSConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context) error
	Cancel()
	Disconnect() error
}

// GitHubConnector is the GitHub connector as the wire drives it.
type GitHubConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context) error
	Cancel()
	Disconnect() error
}

// GoogleConnector is the Google connector as the wire drives it.
type GoogleConnector interface {
	State() connectors.State
	Subscribe() (<-chan connectors.State, func())
	Connect(ctx context.Context, fresh bool) error
	Cancel()
	Disconnect(ctx context.Context) error
	// Project is the Google Cloud project the models run in, empty for none.
	Project() string
}

// LogsService pages and follows a scope's lines: *logs.Service, or a test's fake.
type LogsService interface {
	Fetch(ctx context.Context, id cluster.ClusterID, q logs.FetchRequest) (logs.FetchResponse, error)
	Follow(ctx context.Context, id cluster.ClusterID, q logs.FollowRequest) (logs.FollowResponse, error)
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
