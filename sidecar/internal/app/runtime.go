package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstackhq/kstack/sidecar/graph"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/auth"
	"github.com/kstackhq/kstack/sidecar/internal/services/chat"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/kubeconfig"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/poke"
	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/session"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// Runtime is every service the sidecar builds, in build order: a constructor
// takes only fields above its own. Services never see the Runtime; each takes
// what it needs as arguments.
type Runtime struct {
	DB         *appdb.DB
	Poke       *poke.Service
	Kubeconfig *kubeconfig.Service
	Cluster    cluster.Service
	Auth       auth.Service
	Catalog    catalog.Catalog
	LLM        *llm.Service
	// Shell is nil on a machine with no shell.
	Shell         *bash.Tool
	SandboxStatus sandbox.Status
	Security      *securityconfig.Service
	Memory        memory.Service
	Tools         tools.Box
	Chat          chat.Service

	// parts is added to as each service is built, so start order is build order;
	// stop and close run in reverse, which is what keeps poke's hub open until its
	// subscribers have drained, and app.db open until every service over it has
	// closed.
	parts []lifecycle.Part
}

// build makes every service, in order. A constructor failing closes every part
// built before it, newest first.
func build(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := makeDirs(cfg); err != nil {
		return nil, err
	}

	sb, probed, err := probeSandbox(ctx)
	if err != nil {
		return nil, err
	}
	slog.Info("sandbox probed", "available", probed.Available, "reason", probed.Reason)
	// A nil pointer in an interface is not a nil interface.
	var boxer sandboxer
	if sb != nil {
		boxer = sb
	}
	// Before anything that reads the environment: on macOS the login shell's
	// sets it process-wide, credential plugins resolve against it, and net/http
	// and WebFetch read the proxy variables from it.
	p := pathsOf(cfg)
	launchPath, launchFault := cfg.launchPath, ""
	if cfg.RunLoginShell {
		launchPath, launchFault = launchShell(ctx, boxer, p.Bash.DeniedDirs, p.Bash.TmpDir)
	}

	// The security settings hold no handle, so a failure leaves nothing to close.
	securityStore, err := securityconfig.Open(p.SecurityFile)
	if err != nil {
		return nil, err
	}

	// The app owns app.db and hands it to every service that writes or watches it.
	// It is the first part, so it closes after every service over it.
	db, err := appdb.Open(p.AppDBFile, appdb.DefaultSweepInterval)
	if err != nil {
		return nil, fmt.Errorf("open app database: %w", err)
	}
	rt := &Runtime{DB: db}
	rt.add("app.db", lifecycle.CloseFunc(db.Close))
	fail := func(err error) (*Runtime, error) {
		rt.Close()
		return nil, err
	}

	// Shared cross-subsystem poke bus (wall-clock gap detector + host pokes via
	// gRPC PokeService); see docs/adr/2026-08-09-poke-resync-fanout.md.
	rt.Poke = poke.New()
	rt.add("poke service", lifecycle.StartFunc(rt.Poke.Start))

	// One reader of the user's kubeconfig, shared by everything that resolves a
	// context. Closing it ends every subscription, so it is the app's alone.
	rt.Kubeconfig = kubeconfig.New(cfg.KubeconfigPath, rt.Poke)
	rt.add("kubeconfig service", rt.Kubeconfig)

	rt.Cluster, err = cluster.New(db, p.Cluster, rt.Kubeconfig, rt.Poke)
	if err != nil {
		return fail(err)
	}
	rt.add("cluster service", rt.Cluster)

	keychainService := cfg.KeychainService
	if keychainService == "" {
		keychainService = defaultKeychainService
	}
	rt.Auth, err = auth.New(auth.Config{
		IssuerURL:       cfg.OAuthIssuerURL,
		ClientID:        cfg.OAuthClientID,
		KeychainService: keychainService,
	})
	if err != nil {
		return fail(err)
	}

	rt.Catalog = newCatalog(cfg)
	rt.LLM = llm.New(rt.Catalog.Providers()...)

	pathList := func() securityconfig.RunPath { return securityStore.Get().RunPath() }
	shell, found := bash.New(p.Bash, cfg.HostPID, sb, rt.Cluster, pathList)
	if found && cfg.callDeadline != nil {
		shell.SetCallDeadline(cfg.callDeadline)
	}
	rt.Shell = shell
	rt.SandboxStatus = sandboxStatusOf(found, probed)
	rt.Security = newSecurityService(securityStore, boxer, shell, rt.SandboxStatus, p.Bash.DeniedDirs, launchFault, p.Bash.TmpDir)

	rt.Memory, err = memory.New(db, serverUIDLookup{clusters: rt.Cluster.Clusters()})
	if err != nil {
		return fail(err)
	}
	rt.add("memory service", rt.Memory)

	// The file tools are fenced out of every one of Kstack's directories.
	rt.Tools, err = chatTools(toolDeps{
		shell: shell, fenced: p.Bash.DeniedDirs, hidden: rt.Security.Hidden, umask: cfg.UserUmask,
		memory: rt.Memory, clusters: rt.Cluster,
	})
	if err != nil {
		return fail(fmt.Errorf("fence Kstack's directories: %w", err))
	}

	rt.Chat, err = chat.New(db, p.ChatsDir, p.MonitorDir, rt.LLM, clustercard.New(rt.Cluster), rt.Memory, rt.Tools, rt.Catalog, rt.SandboxStatus, rt.Security)
	if err != nil {
		return fail(err)
	}
	rt.add("chat service", rt.Chat)

	// Edges back to a service built later, set once both exist. The part goes
	// with the edge, so it closes before the service it reads.
	if shell != nil {
		// A probe has no chat, so its run reads the folders granted always alone.
		shell.SetProbeFolders(func(ctx context.Context) []session.Folder { return rt.Chat.FoldersFor(ctx, "") })
		// Ends a running probe at Close.
		rt.add("executable probe", lifecycle.CloseFunc(shell.Close))
	}

	// Before the snapshot, so the first sandboxed run reads the synced list.
	if launchPath != nil && rt.SandboxStatus.Available {
		rt.add("PATH sync", lifecycle.StartFunc(func(ctx context.Context) (func(context.Context) error, error) {
			// Only when the list moved, so nothing runs unasked on a machine
			// whose tools did not; the executable probe part's Close ends it.
			if syncPath(ctx, rt.Security, launchPath) {
				shell.StartProbe(rt.Security.Get().Executables)
			}
			return func(context.Context) error { return nil }, nil
		}))
	}
	// Started after READY and before Serve, so no command can run ahead of it.
	if cfg.RunLoginShell && shell != nil {
		rt.add("shell snapshot", lifecycle.StartFunc(shell.StartSnapshot))
	}
	return rt, nil
}

// add appends a part in build order.
func (rt *Runtime) add(name string, sc lifecycle.StartCloser) {
	rt.parts = append(rt.parts, lifecycle.Part{Name: name, StartCloser: sc})
}

// Close closes every part, newest first. Call it after the stop func Start
// returned, or on a runtime that never started.
func (rt *Runtime) Close() error {
	return lifecycle.CloseAll(rt.parts)
}

// resolver is the GraphQL resolver over the runtime's services.
func (rt *Runtime) resolver() *graph.Resolver {
	// A nil pointer in an interface is not a nil interface.
	var prober graph.ExecutableProber
	if rt.Shell != nil {
		prober = rt.Shell
	}
	return &graph.Resolver{
		ClusterSvc:    rt.Cluster,
		ChatSvc:       rt.Chat,
		MemorySvc:     rt.Memory,
		LLMSvc:        rt.LLM,
		SandboxStatus: rt.SandboxStatus,
		Auth:          rt.Auth,
		SecurityCfg:   rt.Security,
		Executables:   prober,
	}
}
