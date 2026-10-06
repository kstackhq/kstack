// Package app is the sidecar's composition root and lifecycle owner. It builds
// every service into one Runtime, wires the GraphQL and gRPC servers over it,
// and multiplexes them onto one h2c handler.
// main() stays thin: it binds the listener and drives the shutdown surface this
// package exposes — NotifyShutdown / DrainWithContext / Close.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/kstackhq/kstack/sidecar/graph"
	grpcserver "github.com/kstackhq/kstack/sidecar/grpc"
	"github.com/kstackhq/kstack/sidecar/internal/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	agenttool "github.com/kstackhq/kstack/sidecar/internal/tools/agent"
	"github.com/kstackhq/kstack/sidecar/internal/tools/anthropicwebsearch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/edit"
	"github.com/kstackhq/kstack/sidecar/internal/tools/kubequery"
	memorytool "github.com/kstackhq/kstack/sidecar/internal/tools/memory"
	"github.com/kstackhq/kstack/sidecar/internal/tools/read"
	"github.com/kstackhq/kstack/sidecar/internal/tools/taskstop"
	"github.com/kstackhq/kstack/sidecar/internal/tools/webfetch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/write"
)

// defaultKeychainService is the OS-keychain service name used when
// Config.KeychainService is empty; deliberately frontend-neutral.
const defaultKeychainService = "Kstack"

// Config is the subset of process configuration the composition root needs.
// main() resolves these from flags/env and hands them over.
type Config struct {
	// KubeconfigPath is an explicit kubeconfig path; empty uses clientcmd's
	// default-loading rules.
	KubeconfigPath string
	// DataDir holds what a user would lose, CacheDir what Kstack rebuilds, and
	// RuntimeDir what lives for a session. Each is required and absolute.
	DataDir    string
	CacheDir   string
	RuntimeDir string
	// LogDir is where the host logs, which is Kstack's own too: a grant of the
	// home must not open it. Empty when the sidecar logs to stderr alone.
	LogDir string
	// OAuthIssuerURL is the Hydra OAuth issuer base URL; auth derives every
	// endpoint from it via Hydra's standard path layout.
	OAuthIssuerURL string
	// OAuthClientID is the public (PKCE/loopback) OAuth client id.
	OAuthClientID string
	// KeychainService is the OS-keychain service name for the auth token. Empty
	// uses defaultKeychainService; dev runs set a distinct name so dev and
	// release don't share a keychain entry.
	KeychainService string
	// LLMKeys is the model-provider key per provider id, for the keys the config
	// found set; a provider with no key here is not listed.
	LLMKeys map[string]string
	// LLMBaseURLs moves a listed provider's base URL, by provider id. Only a
	// debug build sets it.
	LLMBaseURLs map[string]string
	// AddFake adds the fake to the catalog, last, so a dev run answers with no
	// key. Debug builds only.
	AddFake bool
	// fake is a test's own fake, added as AddFake adds one, so the test can
	// stage its replies.
	fake *llm.Fake
	// HostPID is the host's process, which a command's kill is refused against;
	// 0 when the sidecar was not told it.
	HostPID int
	// RunLoginShell runs the user's login shell: once in New, for its PATH and on
	// macOS its environment, and for the snapshot after Start. main alone sets
	// it, so no test spawns the developer's login shell.
	RunLoginShell bool
	// launchPath is a test's stand-in for the PATH the login shell answers,
	// used while RunLoginShell is false.
	launchPath []string
	// callDeadline is a test's stand-in for what starts a Bash call's
	// deadline, so the test fires the timeout; nil for time.After.
	callDeadline func(d time.Duration) <-chan time.Time
	// UserUmask is the umask the process started with, before main made it
	// owner-only: a file Write makes for the user takes it. Zero on Windows.
	UserUmask fs.FileMode
}

// App owns the composed sidecar: one h2c handler fronting the GraphQL and gRPC
// servers, over the services in its runtime. It is an http.Handler; main() mounts it on
// the listener.
type App struct {
	rt            *Runtime
	handler       http.Handler
	graphqlServer *graph.Server
	grpcServer    *grpcserver.Server
}

// New builds the runtime and the GraphQL and gRPC servers over it, which share
// one h2c socket. ctx is startup's: once it has ended, New stops and answers its
// error.
func New(ctx context.Context, cfg Config) (*App, error) {
	rt, err := build(ctx, cfg)
	if err != nil {
		return nil, err
	}
	graphqlServer := graph.NewServer(rt.resolver())
	grpcServer := grpcserver.NewServer(rt.Auth, rt.Poke)

	mux := http.NewServeMux()
	mux.Handle("/graphql", graphqlServer)

	// gRPC shares the socket with GraphQL via h2c: HTTP/2 application/grpc goes
	// to the gRPC server, everything else to the mux. See
	// docs/adr/2026-08-09-single-socket-h2c.md.
	handler := h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if grpcserver.IsGRPCRequest(r) {
			grpcServer.GRPC().ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}), &http2.Server{})

	return &App{
		rt:            rt,
		handler:       handler,
		graphqlServer: graphqlServer,
		grpcServer:    grpcServer,
	}, nil
}

// makeDirs refuses a directory that is empty or relative, and makes each one
// owner-only when it is missing, which only a run without the host needs.
func makeDirs(cfg Config) error {
	for _, d := range []struct{ flag, path string }{
		{"--data-dir", cfg.DataDir},
		{"--cache-dir", cfg.CacheDir},
		{"--runtime-dir", cfg.RuntimeDir},
	} {
		if !filepath.IsAbs(d.path) {
			return fmt.Errorf("%s must be an absolute path, not %q", d.flag, d.path)
		}
		if err := os.MkdirAll(d.path, 0o700); err != nil {
			return fmt.Errorf("make %s: %w", d.flag, err)
		}
	}
	return nil
}

// ServeHTTP implements http.Handler, dispatching to the h2c multiplexer.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

// Start launches the services' background loops. ctx bounds startup only; the
// returned stop func accepts a drain-deadline context, blocks until all
// background work finishes, and must be called before Close.
func (a *App) Start(ctx context.Context) (func(context.Context) error, error) {
	return lifecycle.StartAll(ctx, a.rt.parts)
}

// NotifyShutdown signals both transports' long-lived streams to close cleanly —
// gRPC streaming handlers return (OK trailers flush) and SSE subscriptions flush
// their terminal frame.
func (a *App) NotifyShutdown() {
	a.grpcServer.NotifyShutdown()
	a.graphqlServer.NotifyShutdown()
}

// DrainWithContext waits for both transports' handlers to unwind or ctx to
// expire. Call after http.Server.Shutdown.
func (a *App) DrainWithContext(ctx context.Context) error {
	errs := make(chan error, 2)
	go func() { errs <- a.graphqlServer.DrainWithContext(ctx) }()
	go func() { errs <- a.grpcServer.DrainWithContext(ctx) }()
	return errors.Join(<-errs, <-errs)
}

// Close releases OS resources after the stop func returns. The gRPC transports go
// first, outside the composed parts: GracefulStop panics on the h2c path, so Stop only
// ever runs here, after the drain.
func (a *App) Close() error {
	a.grpcServer.Stop()
	return a.rt.Close()
}

// serverUIDLookup reads a cluster's last-probed kube-system UID off the cluster
// service, which memory stamps on a cluster memory: "" for a cluster the service
// does not know or has never probed.
type serverUIDLookup struct {
	clusters interface {
		Get(ctx context.Context, id apimeta.ClusterID) (*cluster.Cluster, error)
	}
}

func (u serverUIDLookup) ServerUID(ctx context.Context, id apimeta.ClusterID) string {
	c, err := u.clusters.Get(ctx, id)
	if err != nil || c == nil || c.Status.Server.UID == nil {
		return ""
	}
	return *c.Status.Server.UID
}

// newCatalog is the providers cfg lists: each keyed one at the base URL a debug
// build moved it to, then the fake when asked for.
func newCatalog(cfg Config) catalog.Catalog {
	fake := cfg.fake
	if fake == nil && cfg.AddFake {
		fake = llm.NewFake(llm.FakeChunkDelay)
	}
	return catalog.New(catalog.Config{APIKeys: cfg.LLMKeys, BaseURLs: cfg.LLMBaseURLs, Fake: fake})
}

// probeSandbox is sandbox.Probe, which a test replaces to count its calls.
var probeSandbox = sandbox.Probe

// sandboxer is what the login shell and the security settings need of the
// sandbox: *sandbox.Sandbox, or a test's fake.
type sandboxer interface {
	Command(ctx context.Context, r sandbox.Run) (*exec.Cmd, error)
	System(home, shell string) sandbox.System
	Never(home string) []string
}

// newSecurityService is the security settings and the frozen PATH kept in
// them. The sync judges an entry, and a grant a folder, by what a sandboxed run
// of the bash tool's shell reads, and by the paths no rule opens, Kstack's
// directories (denied) among them. Refresh PATH runs the login shell in sb as
// the launch does, its TMPDIR under tmpDir. Each reads the denied-always list
// afresh, as a run does: it lists the other users' homes, which can appear
// while the sidecar runs. On a machine with no sandbox it syncs nothing, keeps
// no fault and refuses every folder.
func newSecurityService(store *securityconfig.Store, sb sandboxer, shell *bash.Tool, status sandbox.Status, denied []string, fault, tmpDir string) *securityconfig.Service {
	if !status.Available {
		return securityconfig.NewService(store, nil, nil, "")
	}
	home, _ := os.UserHomeDir()
	zones := func() securityconfig.Zones {
		return securityconfig.Zones{
			Never: slices.Concat(sb.Never(home), denied), Open: sb.System(home, shell.Shell()).Files, Home: home, NoWrite: sandbox.NoWrite(home),
		}
	}
	return securityconfig.NewService(store, zones, shellPathResolver(sb, home, denied, tmpDir), fault)
}

// syncPath folds the launch's PATH into the stored list, and answers whether
// it changed it. A sync that fails changes nothing and is not a startup
// error.
func syncPath(ctx context.Context, svc *securityconfig.Service, path []string) bool {
	changed, err := svc.SyncPath(ctx, path)
	if err != nil {
		slog.Warn("PATH not synced", "err", err)
	}
	return changed
}

// sandboxStatusOf is whether sandboxed Bash is offered: a sandbox with no shell
// has no command to confine.
func sandboxStatusOf(shellFound bool, probed sandbox.Status) sandbox.Status {
	if !shellFound {
		return sandbox.Status{Reason: "no shell was found"}
	}
	return probed
}

// toolDeps is what the tool box is built over.
type toolDeps struct {
	// shell is nil on a machine with no shell.
	shell *bash.Tool
	// fenced is Kstack's directories, which the file tools stay out of.
	fenced []string
	// hidden answers what the sandbox keeps shut under a grant.
	hidden func() (never, shut []string)
	// umask is what a file Write makes for the user takes.
	umask    fs.FileMode
	memory   memory.Service
	clusters cluster.Service
}

// chatTools is the one box: bash where New found a shell, Read, Memory, Write, Edit
// and WebFetch on every machine, TaskStop for the tasks bash starts, then the
// provider's web search, then KubeQuery. WebFetch dials no local or private address
// but the proxy the environment names. The order is the preference within a kind:
// the first tool of a kind that a turn's target takes is the one it gets. A machine
// with no shell still reads the stored calls of bash and TaskStop.
func chatTools(d toolDeps) (tools.Box, error) {
	reader, err := read.New(d.hidden, d.fenced...)
	if err != nil {
		return tools.Box{}, err
	}
	writer, err := write.New(d.umask, d.hidden, d.fenced...)
	if err != nil {
		return tools.Box{}, err
	}
	editor, err := edit.New(d.hidden, d.fenced...)
	if err != nil {
		return tools.Box{}, err
	}
	fetcher := webfetch.New(webfetch.NewTransport(webfetch.Dialing{
		Resolver: net.DefaultResolver,
		Public:   webfetch.Public,
		Proxy:    httpproxy.FromEnvironment(),
	}), webfetch.FetchTimeout)
	search := anthropicwebsearch.New(time.Now)
	notes := memorytool.New(d.memory)
	query := kubequery.New(d.clusters)
	if d.shell == nil {
		return tools.NewBox([]tools.Tool{reader, notes, writer, editor, fetcher, agenttool.New(), search, query}, bash.Reader{}, taskstop.New()), nil
	}
	return tools.NewBox([]tools.Tool{d.shell, reader, notes, writer, editor, fetcher, taskstop.New(), agenttool.New(), search, query}), nil
}
