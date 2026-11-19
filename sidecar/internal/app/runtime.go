package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/kstackhq/kstack/sidecar/graph"
	"github.com/kstackhq/kstack/sidecar/internal/agent/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/agent/chat"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/filelock"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/lib/oauth/browser"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
	"github.com/kstackhq/kstack/sidecar/internal/services/auth"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/catalogsync"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/chatgptauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/oauthconn"
	"github.com/kstackhq/kstack/sidecar/internal/services/kubeconfig"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
	"github.com/kstackhq/kstack/sidecar/internal/services/memory"
	"github.com/kstackhq/kstack/sidecar/internal/services/poke"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
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
	// Logs reads views' lines through the cluster service and owns each cluster
	// cache's log file, which the cluster service clears and removes with the cache.
	Logs *logs.Service
	Auth auth.Service
	// ChatGPT is Sign in with ChatGPT, which signs the chatgpt provider.
	ChatGPT *oauthconn.Service
	Catalog catalog.Catalog
	LLM     *llm.Service
	// Shell is nil on a machine with no shell.
	Shell         *bash.Tool
	SandboxStatus sandbox.Status
	// CloudTargets are the cloud platforms this build has, in Settings' order.
	CloudTargets []permissions.Target
	Settings     *settings.Service
	Memory       memory.Service
	Tools        tools.Box
	Chat         chat.Service

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
	p := pathsOf(cfg)

	// One sidecar per data directory: the lock is taken before anything opens
	// a file there. This text reaches the user.
	lock, err := filelock.TryLock(p.LockFile)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, fmt.Errorf("another Kstack is using %s", cfg.DataDir)
	}
	if err != nil {
		return nil, fmt.Errorf("lock the data directory: %w", err)
	}
	rt := &Runtime{}
	// The first part, so it closes after every file it guards. Once: a runtime
	// may be closed twice, and a file cannot.
	rt.add("data directory lock", lifecycle.CloseFunc(sync.OnceValue(lock.Close)))
	fail := func(err error) (*Runtime, error) {
		rt.Close()
		return nil, err
	}

	sb, probed, err := probeSandbox(ctx)
	if err != nil {
		return fail(err)
	}
	slog.Info("sandbox probed", "available", probed.Available, "reason", probed.Reason)
	// A nil pointer in an interface is not a nil interface.
	var boxer sandboxer
	if sb != nil {
		boxer = sb
	}
	// Before anything that reads the environment: on macOS the login shell's
	// sets it process-wide, credential plugins resolve against it, and net/http
	// and WebFetch read the proxy variables from it. The keys it answers reach
	// the catalog alone.
	login := launch{path: cfg.launchPath}
	if cfg.RunLoginShell {
		login = launchShell(ctx, boxer, p.Bash.DeniedDirs, p.Bash.TmpDir)
		cfg.LLMKeys = withShellKeys(cfg.LLMKeys, login.keys)
	}

	// The settings hold no handle, so a failure leaves the sandbox and the lock to close.
	settingsStore, err := settings.Open(p.SettingsFile)
	if err != nil {
		_ = sb.Close()
		return fail(err)
	}

	// The app owns app.db and hands it to every service that writes or watches it.
	// It closes after every service over it.
	db, err := appdb.Open(p.AppDBFile, appdb.DefaultSweepInterval)
	if err != nil {
		_ = sb.Close()
		// This text reaches the user: a newer file names itself and both versions.
		if newer, ok := errors.AsType[*sqlitemigrate.NewerSchemaError](err); ok {
			return fail(fmt.Errorf("app.db was written by a newer Kstack (schema %d; this build reads up to %d)", newer.Recorded, newer.Supported))
		}
		return fail(fmt.Errorf("open app database: %w", err))
	}
	rt.DB = db
	rt.add("app.db", lifecycle.CloseFunc(db.Close))
	// Its close ends every sandboxed run still going, after every service
	// over the sandbox has stopped its own.
	rt.add("sandbox", lifecycle.CloseFunc(sb.Close))

	// Shared cross-subsystem poke bus (wall-clock gap detector + host pokes via
	// gRPC PokeService); see docs/adr/2026-08-09-poke-resync-fanout.md.
	rt.Poke = poke.New()
	rt.add("poke service", lifecycle.StartFunc(rt.Poke.Start))

	// One reader of the user's kubeconfig, shared by everything that resolves a
	// context. Closing it ends every subscription, so it is the app's alone.
	rt.Kubeconfig = kubeconfig.New(cfg.KubeconfigPath, rt.Poke)
	rt.add("kubeconfig service", rt.Kubeconfig)

	rt.Cluster, err = cluster.New(db, p.Cluster, rt.Kubeconfig, rt.Poke, cluster.WithFaults(cfg.Faults))
	if err != nil {
		return fail(err)
	}
	rt.add("cluster service", rt.Cluster)

	// The edge back: the cluster service's cache pass clears and removes a cache's log
	// file through the logs service, which closes first and still deletes after that.
	rt.Logs = logs.New(rt.Cluster, p.LogsDir, logstore.DefaultRetention)
	rt.add("logs service", lifecycle.CloseFunc(rt.Logs.Close))
	rt.Cluster.SetCacheFiles(rt.Logs)

	keychainService := cfg.KeychainService
	if keychainService == "" {
		keychainService = defaultKeychainService
	}
	rt.Auth, err = auth.New(auth.Config{
		IssuerURL:       cfg.OAuthIssuerURL,
		ClientID:        cfg.OAuthClientID,
		KeychainService: keychainService,
	})
	if err != nil { // coverage:ignore auth.New never returns an error
		return fail(err)
	}

	rt.ChatGPT, err = oauthconn.New(oauthconn.Config{
		Provider:        movedProvider(chatgptauth.Provider(), cfg),
		DB:              db,
		KeychainService: keychainService,
	})
	if err != nil { // coverage:ignore the statements prepare on the schema app.db was just migrated to
		return fail(err)
	}
	rt.add("chatgpt service", lifecycle.CloseFunc(func() error { rt.ChatGPT.Close(); return nil }))

	rt.Catalog = newCatalog(cfg, map[string]llm.SessionSigner{chatgptauth.ID: rt.ChatGPT})
	rt.LLM = llm.New(rt.Catalog.Providers()...)
	rt.add("chatgpt catalog", lifecycle.StartFunc(func(context.Context) (func(context.Context) error, error) {
		stop := catalogsync.Start(chatgptauth.ID, rt.ChatGPT, rt.Poke, rt.LLM, catalog.ReadSignedModels)
		return func(context.Context) error { stop(); return nil }, nil
	}))

	pathList := func() settings.RunPath { return settingsStore.Get().RunPath() }
	platforms := cloudPlatformsFor(probed, cfg.cloudPlatforms, rt.Kubeconfig)
	shell, found := bash.New(p.Bash, cfg.HostPID, sb, rt.Cluster, pathList, bash.Cloud{Platforms: platforms, On: settingsStore.CloudOn})
	if found && cfg.callDeadline != nil {
		shell.SetCallDeadline(cfg.callDeadline)
	}
	rt.Shell = shell
	rt.SandboxStatus = sandboxStatusOf(found, probed)
	rt.CloudTargets = cloudTargets(platforms)
	rt.Settings = newSettingsService(settingsStore, boxer, shell, rt.SandboxStatus, p.Bash.DeniedDirs, login.fault, p.Bash.TmpDir)

	rt.Memory, err = memory.New(db, serverUIDLookup{clusters: rt.Cluster.Clusters()})
	if err != nil {
		return fail(err)
	}
	rt.add("memory service", rt.Memory)

	// The file tools are fenced out of every one of Kstack's directories.
	rt.Tools, err = chatTools(toolDeps{
		shell: shell, fenced: p.Bash.DeniedDirs, hidden: rt.Settings.Hidden, umask: cfg.UserUmask,
		memory: rt.Memory, clusters: rt.Cluster, logs: rt.Logs,
	})
	if err != nil { // coverage:ignore pathsOf always names Kstack's three directories
		return fail(fmt.Errorf("fence Kstack's directories: %w", err))
	}

	rt.Chat, err = chat.New(db, p.ChatsDir, p.MonitorDir, rt.LLM, clustercard.New(rt.Cluster), rt.Memory, rt.Tools, rt.Catalog, rt.SandboxStatus, rt.Settings, rt.CloudTargets)
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
	if login.path != nil && rt.SandboxStatus.Available {
		rt.add("PATH sync", lifecycle.StartFunc(func(ctx context.Context) (func(context.Context) error, error) {
			// Only when the list moved, so nothing runs unasked on a machine
			// whose tools did not; the executable probe part's Close ends it.
			if syncPath(ctx, rt.Settings, login.path) {
				shell.StartProbe(rt.Settings.Get().Executables)
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
		ChatGPT:       rt.ChatGPT,
		OpenURL:       browser.Open,
		Settings:      rt.Settings,
		Executables:   prober,
		Logs:          rt.Logs,
		CloudTargets:  rt.CloudTargets,
	}
}

// cloudPlatformsFor is the cloud platforms a sandboxed run may reach: the
// build's, over kc, or a test's in their place. A machine with no sandbox has
// none, so it offers no switch.
func cloudPlatformsFor(status sandbox.Status, configured []bash.CloudPlatform, kc *kubeconfig.Service) []bash.CloudPlatform {
	switch {
	case !status.Available:
		return nil
	case configured != nil:
		return configured
	}
	return builtCloudPlatforms(kc)
}

// cloudTargets is each cloud platform's target, in the build's order.
func cloudTargets(platforms []bash.CloudPlatform) []permissions.Target {
	targets := make([]permissions.Target, 0, len(platforms))
	for _, p := range platforms {
		targets = append(targets, p.Target())
	}
	return targets
}

// movedProvider is p at the issuer and the base URL a debug build moved it to.
func movedProvider(p oauthconn.Provider, cfg Config) oauthconn.Provider {
	p.Issuer = cmp.Or(cfg.ConnectorIssuerURLs[p.ID], p.Issuer)
	p.APIBaseURL = cmp.Or(cfg.LLMBaseURLs[p.ID], p.APIBaseURL)
	return p
}
