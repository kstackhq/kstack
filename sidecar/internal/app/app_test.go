package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/securityconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kstackhq/kstack/sidecar/grpc/authpb"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/lib/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/anthropicwebsearch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/taskstop"
)

// Read, Memory, Write, Edit and WebFetch work on every machine, so a machine with no shell is
// offered them alone.
func TestTheFileToolsAreOfferedWithoutAShell(t *testing.T) {
	box, err := chatTools(toolDeps{fenced: []string{t.TempDir()}, umask: 0o022})
	require.NoError(t, err)

	defs, native := box.Offer()
	assert.Equal(t, []string{"Read", "Memory", "Write", "Edit", "WebFetch", "Agent", "KubeQuery"}, definitionNames(defs))
	require.Len(t, native, 1)
	assert.Equal(t, anthropicwebsearch.Name, native[0].Tool.Name())
	got, ok := box.Action(bash.Name, json.RawMessage(`{"command":"ls"}`), "/srv", false)
	assert.True(t, ok, "a stored bash call still shows")
	assert.Equal(t, "ls", got.Command.Text)
}

// A machine with no shell still knows what every stored call did, a TaskStop
// included.
func TestAStoredCallsKindIsKnownWithoutAShell(t *testing.T) {
	box, err := chatTools(toolDeps{fenced: []string{t.TempDir()}, umask: 0o022})
	require.NoError(t, err)

	for name, want := range map[string]tools.ActionKind{taskstop.Name: tools.ActionStop, bash.Name: tools.ActionCommand} {
		got, ok := box.ActionKind(name)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
}

// definitionNames is each definition's name, in order.
func definitionNames(defs []llm.ToolDefinition) []string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

// Every model of every catalog is offered each contract it lists, and an offer
// its target's Stream accepts: a contract the box cannot offer, or a call name
// spelled twice, shows up here rather than on a user's turn.
func TestEveryCatalogTargetTakesItsBox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"refused"}}`))
	}))
	t.Cleanup(srv.Close)
	box := everyTool(t)
	lists := everyCatalog()

	for _, p := range lists.Providers() {
		p.BaseURL = srv.URL
		for _, m := range p.Catalog {
			target := llm.Target{Provider: p, Model: m}
			turn := box.For(target, lists.ToolsFor(target))

			defs, native := turn.Offer()
			_, err := target.Stream(t.Context(), "", nil, "", defs, native, func(llm.Chunk) {})
			assert.NotErrorIs(t, err, llm.ErrToolsUnsupported, "%s/%s", p.ID, m.ID)
		}
	}
}

// everyCatalog is the catalog with every provider it can list, each keyed.
func everyCatalog() catalog.Catalog {
	keys := map[string]string{}
	for id := range catalog.KeyVars() {
		keys[id] = "key"
	}
	return catalog.New(catalog.Config{APIKeys: keys, Fake: llm.NewFake(0)})
}

// everyToolTarget is every target of cat whose model takes tools.
func everyToolTarget(cat catalog.Catalog) []llm.Target {
	var out []llm.Target
	for _, p := range cat.Providers() {
		for _, m := range p.Catalog {
			if m.Tools {
				out = append(out, llm.Target{Provider: p, Model: m})
			}
		}
	}
	return out
}

// everyTool is the box of a machine with a shell: every tool the app builds.
func everyTool(t *testing.T) tools.Box {
	t.Helper()
	box, err := chatTools(toolDeps{shell: &bash.Tool{}, fenced: []string{t.TempDir()}, umask: 0o022})
	require.NoError(t, err)
	return box
}

// offerNames is the name of every tool box offers, in box order.
func offerNames(box tools.Box) []string {
	defs, native := box.Offer()
	names := definitionNames(defs)
	for _, o := range native {
		names = append(names, o.Tool.Name())
	}
	return names
}

// Every target is offered exactly its list: each listed name is a tool the app
// builds, and each listed vendor tool rides that target's wire.
func TestEachTargetIsOfferedItsList(t *testing.T) {
	box := everyTool(t)
	lists := everyCatalog()

	for _, target := range everyToolTarget(lists) {
		turn := box.For(target, lists.ToolsFor(target))
		assert.ElementsMatch(t, lists.ToolsFor(target), offerNames(turn), "%s/%s", target.Provider.ID, target.Model.ID)
	}
}

// No list names two tools of one kind, and every list covers each kind our
// tools cover.
func TestEachListIsOneToolOfEachKind(t *testing.T) {
	box := everyTool(t)
	lists := everyCatalog()
	defs, _ := box.Offer()
	var ours []tools.ActionKind
	for _, name := range definitionNames(defs) {
		k, ok := box.ActionKind(name)
		require.True(t, ok, name)
		ours = append(ours, k)
	}

	for _, target := range everyToolTarget(lists) {
		id := target.Provider.ID + "/" + target.Model.ID
		var kinds []tools.ActionKind
		for _, name := range lists.ToolsFor(target) {
			k, ok := box.ActionKind(name)
			require.True(t, ok, "%s lists %s", id, name)
			assert.NotContains(t, kinds, k, "%s lists two tools of kind %s", id, k)
			kinds = append(kinds, k)
		}
		assert.Subset(t, kinds, ours, id)
	}
}

// Every tool the app builds is on a list, so a new tool left off every list
// fails here rather than never being offered.
func TestEveryToolIsOnAList(t *testing.T) {
	lists := everyCatalog()
	listed := map[string]bool{}
	for _, target := range everyToolTarget(lists) {
		for _, name := range lists.ToolsFor(target) {
			listed[name] = true
		}
	}

	for _, name := range offerNames(everyTool(t)) {
		assert.True(t, listed[name], name)
	}
}

// newTestApp builds an App backed by a kubeconfig with two contexts, then serves
// it over httptest (HTTP/1.1 for GraphQL/SSE, h2c upgrade for gRPC — exactly the
// production split). It does NOT call Start(), so the cluster-cache coordinator
// never runs; the lifecycle surface is what's under test.
func newTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	writeKubeconfig(t, kubeconfig, "context-A", "context-B")

	a, err := New(t.Context(), withDirs(t, Config{
		KubeconfigPath: kubeconfig,
		// A real data dir so app.db lands in the per-test temp dir — with an
		// empty DataDir New would create app.db relative to the test's
		// working directory (the package dir).
		DataDir: t.TempDir(),
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	return a, kubeconfig
}

// withDirs gives a config the directories it leaves empty, each a temp
// directory of its own.
func withDirs(t *testing.T, cfg Config) Config {
	t.Helper()
	for _, dir := range []*string{&cfg.DataDir, &cfg.CacheDir, &cfg.RuntimeDir} {
		if *dir == "" {
			*dir = t.TempDir()
		}
	}
	return cfg
}

// Each directory is required and absolute: a relative one would name another
// file from each working directory, and the fence and a chat's paths compare
// absolute paths.
func TestNewRefusesAnEmptyOrRelativeDirectory(t *testing.T) {
	for _, flag := range []string{"--data-dir", "--cache-dir", "--runtime-dir"} {
		for _, dir := range []string{"", "relative"} {
			t.Run(flag+"="+dir, func(t *testing.T) {
				t.Chdir(t.TempDir())
				cfg := withDirs(t, Config{})
				switch flag {
				case "--data-dir":
					cfg.DataDir = dir
				case "--cache-dir":
					cfg.CacheDir = dir
				case "--runtime-dir":
					cfg.RuntimeDir = dir
				}
				_, err := New(t.Context(), cfg)
				require.ErrorContains(t, err, flag)
				assert.NoDirExists(t, "relative")
			})
		}
	}
}

// writeKubeconfig writes a kubeconfig at path naming each context over one
// cluster and user.
func writeKubeconfig(t *testing.T, path string, contexts ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\ncurrent-context: context-A\ncontexts:\n")
	for _, name := range contexts {
		fmt.Fprintf(&b, "- name: %s\n  context: {cluster: c, user: u}\n", name)
	}
	b.WriteString("clusters:\n- name: c\n  cluster: {server: https://example}\nusers:\n- name: u\n  user: {}\n")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
}

// graphql POSTs one query to the app and returns the raw response.
func graphql(t *testing.T, srvURL, query string) string {
	t.Helper()
	raw, err := postGraphQL(srvURL, query)
	require.NoError(t, err)
	return raw
}

// postGraphQL is graphql answering its error, for a condition that runs off
// the test's goroutine.
func postGraphQL(srvURL, query string) (string, error) {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest(http.MethodPost, srvURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return string(raw), err
}

// startApp starts a and registers its stop.
func startApp(t *testing.T, a *App) {
	t.Helper()
	stop, err := a.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout)
		defer cancel()
		assert.NoError(t, stop(ctx))
	})
}

// The chat surface answers over the app's own handler: the composition root is the
// one place the resolver's chat service is set, and an unwired one panics rather
// than refusing. The id is well-formed, so the refusal is the lookup's.
func TestAppServesTheChatSurface(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)

	raw := graphql(t, srv.URL, `mutation { chatRename(id: "`+appdb.NewID()+`", title: "t") { id } }`)

	assert.Contains(t, raw, `"code":"KSTACK_RECORD_NOT_FOUND"`)
}

// The whole cluster lifecycle through the app: a context is imported, toggled, chatted
// under, and deleted once it has left the file. The mark tears the runtime and the
// chats down behind it, and the row goes last.
func TestAppTearsDownADeletedCluster(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	writeKubeconfig(t, path, "context-A", "context-B")
	a, err := New(t.Context(), withDirs(t, Config{KubeconfigPath: path, DataDir: dir, AddFake: true}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	startApp(t, a)
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)

	// converge bounds each wait: a file watcher, beehive's queue and the mirror's
	// pass sit between a write and its effect.
	const converge = 10 * time.Second
	clusterFor := func(contextName string) (id string, present bool, ok bool) {
		var resp struct {
			Data struct {
				Clusters []struct {
					ID   string `json:"id"`
					Spec struct {
						Source struct {
							Kubeconfig *struct {
								Context string `json:"context"`
							} `json:"kubeconfig"`
						} `json:"source"`
					} `json:"spec"`
					Status struct {
						Source struct {
							Kubeconfig *struct {
								IsPresent bool `json:"isPresent"`
							} `json:"kubeconfig"`
						} `json:"source"`
					} `json:"status"`
				} `json:"clusters"`
			} `json:"data"`
		}
		raw := graphql(t, srv.URL, `{ clusters { id spec { source { kubeconfig { context } } } status { source { kubeconfig { isPresent } } } } }`)
		require.NoError(t, json.Unmarshal([]byte(raw), &resp), raw)
		for _, c := range resp.Data.Clusters {
			if c.Spec.Source.Kubeconfig != nil && c.Spec.Source.Kubeconfig.Context == contextName {
				observed := c.Status.Source.Kubeconfig
				return c.ID, observed != nil && observed.IsPresent, true
			}
		}
		return "", false, false
	}

	var id string
	require.Eventually(t, func() bool {
		var present, ok bool
		id, present, ok = clusterFor("context-B")
		return ok && present
	}, converge, 10*time.Millisecond, "context-B imported and observed present")

	raw := graphql(t, srv.URL, `mutation { clusterSyncEnabledSet(id: "`+id+`", syncEnabled: false) { spec { syncEnabled } } }`)
	assert.Contains(t, raw, `"syncEnabled":false`)

	raw = graphql(t, srv.URL, `mutation { chatSend(mode: Chat, clusterID: "`+id+`", sandboxDisabled: false, networkEnabled: false, networkThisTurn: false, providerID: "fake", modelID: "fake", effort: "high", requestID: "`+appdb.NewID()+`", content: "hello") { chatID } }`)
	var sent struct {
		Data struct {
			ChatSend struct {
				ChatID string `json:"chatID"`
			} `json:"chatSend"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &sent), raw)
	chatID := sent.Data.ChatSend.ChatID
	require.NotEmpty(t, chatID, raw)

	// While the file still declares the context the delete is refused.
	raw = graphql(t, srv.URL, `mutation { clusterDelete(id: "`+id+`") }`)
	assert.Contains(t, raw, "KSTACK_CONFLICT")

	writeKubeconfig(t, path, "context-A")
	require.Eventually(t, func() bool {
		_, present, ok := clusterFor("context-B")
		return ok && !present
	}, converge, 10*time.Millisecond, "context-B observed absent")

	raw = graphql(t, srv.URL, `mutation { clusterDelete(id: "`+id+`") }`)
	assert.Contains(t, raw, `"clusterDelete":true`)
	assert.Contains(t, graphql(t, srv.URL, `mutation { clusterDelete(id: "`+id+`") }`), `"clusterDelete":true`, "a repeat")

	require.Eventually(t, func() bool {
		_, _, ok := clusterFor("context-B")
		return !ok
	}, converge, 10*time.Millisecond, "the row removed once the runtime and the chats are gone")
	assert.Contains(t, graphql(t, srv.URL, `mutation { chatRename(id: "`+chatID+`", title: "t") { id } }`), "KSTACK_RECORD_NOT_FOUND")
}

// The cluster reconciles read the kubeconfig on their first pass and defer while it
// reports unread, which is a state only this ordering keeps them out of: Start reads
// synchronously, so a service started after it always observes a read. Reversed, every
// record would observe its context absent and orphan itself — a mass write, not a
// pause — and the guards in cluster are the only thing that would stand between the
// reordering and that.
func TestAppStartsKubeconfigBeforeTheClusterService(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	assert.Less(t, partIndex(t, a, "kubeconfig service"), partIndex(t, a, "cluster service"))
}

// The chat service is a part like the others: built by New, started and stopped
// with the app.
func TestAppStartsAndStopsWithTheChatService(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	require.GreaterOrEqual(t, partIndex(t, a, "chat service"), 0)

	stop, err := a.Start(t.Context())
	require.NoError(t, err)
	require.NoError(t, stop(t.Context()))
}

// The memory service starts before the chat service and so stops after it: a
// turn reads the index and saves through it until the chat service has stopped.
func TestTheMemoryServiceOutlivesTheChatService(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	assert.Less(t, partIndex(t, a, "memory service"), partIndex(t, a, "chat service"))
}

// The UIDs memory stamps are the cluster service's last probe; a cluster it
// does not know, or has never probed, has none.
func TestServerUIDLookupReadsTheClusterService(t *testing.T) {
	uid := "uid-1"
	uids := serverUIDLookup{clusters: fakeClusters{"probed": {Status: cluster.ClusterStatus{Server: cluster.ClusterServer{UID: &uid}}}, "unprobed": {}}}

	assert.Equal(t, "uid-1", uids.ServerUID(t.Context(), "probed"))
	assert.Empty(t, uids.ServerUID(t.Context(), "unprobed"))
	assert.Empty(t, uids.ServerUID(t.Context(), "unknown"))
}

type fakeClusters map[apimeta.ClusterID]*cluster.Cluster

func (f fakeClusters) Get(_ context.Context, id apimeta.ClusterID) (*cluster.Cluster, error) {
	return f[id], nil
}

// partIndex returns where the named part sits in start order.
func partIndex(t *testing.T, a *App, name string) int {
	t.Helper()
	i := slices.IndexFunc(a.rt.parts, func(p lifecycle.Part) bool { return p.Name == name })
	require.GreaterOrEqual(t, i, 0, "no part named %q", name)
	return i
}

// With no OAuth config (the standalone/test default), the account surface is
// wired through composition but degraded: the authState query answers signed-out
// instead of panicking. This is also the canary that the composed App is a
// working http.Handler with the GraphQL surface wired through composition.
func TestAppAuthStateDegradesSignedOut(t *testing.T) {
	a, _ := newTestApp(t)
	ts := httptest.NewServer(a)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/graphql", "application/json",
		strings.NewReader(`{"query":"{ authState { authenticated identity { sub } } }"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(raw), `"identity":null`)
	assert.Contains(t, string(raw), `"authenticated":false`)
}

// TestAppShutdownDrainsBothTransports is the heart of the lifecycle contract:
// with a live SSE subscription AND a live gRPC AuthStateWatch stream open,
// NotifyShutdown signals both to close and DrainWithContext returns nil only once
// both handlers have unwound. If either transport were left dangling, the GraphQL
// WaitGroup or the gRPC stream WaitGroup would never reach zero and
// DrainWithContext would hit its deadline instead.
func TestAppShutdownDrainsBothTransports(t *testing.T) {
	a, _ := newTestApp(t)
	ts := httptest.NewServer(a)
	defer ts.Close()

	// Live SSE subscription.
	sseReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/graphql", strings.NewReader(`{"query":"subscription { authStateWatch { authenticated } }"}`))
	sseReq.Header.Set("Content-Type", "application/json")
	sseReq.Header.Set("Accept", "text/event-stream")
	sseResp, err := http.DefaultClient.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()
	buf := make([]byte, 1)
	_, err = sseResp.Body.Read(buf) // ensure the stream is established
	require.NoError(t, err)

	// Live gRPC AuthStateWatch stream over h2c on the same listener.
	conn, err := grpc.NewClient(strings.TrimPrefix(ts.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	stream, err := authpb.NewAuthServiceClient(conn).AuthStateWatch(context.Background(), &authpb.AuthStateWatchRequest{})
	require.NoError(t, err)
	snap, err := stream.Recv()
	require.NoError(t, err)
	assert.False(t, snap.GetAuthenticated())

	grpcRecvErr := make(chan error, 1)
	go func() {
		_, e := stream.Recv()
		grpcRecvErr <- e
	}()

	// The two-line app shutdown surface main.go drives.
	a.NotifyShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, a.DrainWithContext(ctx))

	// The gRPC stream ended cleanly (OK trailers → EOF), not cut mid-flight.
	assert.ErrorIs(t, testutil.Recv(t, grpcRecvErr, "the gRPC Watch to drain"), io.EOF)
}

// A data dir that is not a directory fails at the first subsystem that needs
// one: New reports it rather than returning a half-built App.
func TestAppRejectsAnUnusableDataDir(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	_, err := New(t.Context(), withDirs(t, Config{DataDir: notADir}))
	require.Error(t, err)
}

// The app opens app.db once, first in parts so the reverse close order releases it
// after every service. SQLite deletes the -wal beside a database when its last
// connection closes, so the sibling's absence shows the file was released.
func TestAppOwnsTheDatabase(t *testing.T) {
	dir := t.TempDir()
	a, err := New(t.Context(), withDirs(t, Config{DataDir: dir}))
	require.NoError(t, err)
	assert.Equal(t, 0, partIndex(t, a, "app.db"))

	wal := filepath.Join(dir, "app.db-wal")
	require.FileExists(t, wal)
	require.NoError(t, a.Close())
	assert.NoFileExists(t, wal)
}

// A directory a service cannot make fails the build and closes app.db: the
// chats' for the chat service.
func TestAppClosesTheDatabaseWhenADirectoryFails(t *testing.T) {
	cfg := withDirs(t, Config{})
	require.NoError(t, os.WriteFile(filepath.Join(cfg.DataDir, "chats"), nil, 0o600))

	_, err := New(t.Context(), cfg)
	require.ErrorContains(t, err, "open the chats' directory")
	assert.NoFileExists(t, filepath.Join(cfg.DataDir, "app.db-wal"))
}

// A constructor failing after the open closes the file. A service's statements fail
// to prepare on a database whose migration is recorded but whose tables are gone.
func TestAppClosesTheDatabaseWhenAConstructorFails(t *testing.T) {
	for table, service := range map[string]string{"clusters": "cluster", "approvals": "chat", "memories": "memory"} {
		t.Run(service, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app.db")
			db, err := appdb.Open(path, 0)
			require.NoError(t, err)
			_, err = db.Write.Exec(`DROP TABLE ` + table)
			require.NoError(t, err)
			require.NoError(t, db.Close())

			_, err = New(t.Context(), withDirs(t, Config{DataDir: dir}))
			require.ErrorContains(t, err, table)
			assert.NoFileExists(t, path+"-wal")
		})
	}
}

// A sandbox file that is not a JSON object fails New naming it, before app.db
// opens.
func TestABadSecurityFileFailsNew(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "security.json")
	require.NoError(t, os.WriteFile(file, []byte("{"), 0o600))

	_, err := New(t.Context(), withDirs(t, Config{DataDir: dir}))
	require.ErrorContains(t, err, file)
	assert.NoFileExists(t, filepath.Join(dir, "app.db"))
}

// The catalog is the config's: each keyed provider at the base URL a debug build
// moved it to, then the fake when asked for.
func TestTheCatalogIsTheConfigs(t *testing.T) {
	providers := newCatalog(Config{
		LLMKeys:     map[string]string{"anthropic": "sk-ant-test"},
		LLMBaseURLs: map[string]string{"anthropic": "http://127.0.0.1:1"},
		AddFake:     true,
	}).Providers()

	require.Len(t, providers, 2)
	assert.Equal(t, "sk-ant-test", providers[0].Key)
	assert.Equal(t, "http://127.0.0.1:1", providers[0].BaseURL)
	assert.Equal(t, "fake", providers[1].ID)
}

// The fake is a debug build's alone, and last: a release-shaped config lists only
// the rows it was given.
func TestTheFakeIsListedOnlyWhenAsked(t *testing.T) {
	// The models are the catalogs flattened, so the providers are read off them in
	// order, each named once however many models it lists.
	providers := func(addFake bool) []string {
		a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir(), LLMKeys: map[string]string{"anthropic": "sk-ant-test"}, AddFake: addFake}))
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, a.Close()) })
		ts := serveApp(t, a)
		var body struct {
			Data struct {
				Models []struct{ Provider struct{ ID string } }
			}
		}
		require.NoError(t, json.Unmarshal([]byte(graphql(t, ts.URL, `{ models { provider { id } } }`)), &body))
		var ids []string
		for _, m := range body.Data.Models {
			if len(ids) == 0 || ids[len(ids)-1] != m.Provider.ID {
				ids = append(ids, m.Provider.ID)
			}
		}
		return ids
	}

	assert.Equal(t, []string{"anthropic"}, providers(false))
	assert.Equal(t, []string{"anthropic", "fake"}, providers(true))
}

// Sandboxed Bash is offered where a shell was found and the probe found a
// sandbox; the reason is the probe's, or that there is no shell.
func TestTheSandboxStatusIsTheShellAndTheProbe(t *testing.T) {
	found := sandbox.Status{Available: true, Reason: "bwrap at /usr/bin/bwrap"}
	missing := sandbox.Status{Reason: "bwrap was not found"}

	assert.Equal(t, sandbox.Status{Available: true, Reason: "bwrap at /usr/bin/bwrap"}, sandboxStatusOf(true, found))
	assert.Equal(t, sandbox.Status{Reason: "bwrap was not found"}, sandboxStatusOf(true, missing))
	assert.Equal(t, sandbox.Status{Reason: "no shell was found"}, sandboxStatusOf(false, found))
}

// A sandbox with no shell confines no command, so it offers no network either.
func TestNoShellOffersNoNetwork(t *testing.T) {
	found := sandbox.Status{Available: true, Reason: "bwrap at /usr/bin/bwrap", NetworkAvailable: true}

	assert.Equal(t, found, sandboxStatusOf(true, found))
	got := sandboxStatusOf(false, found)
	assert.False(t, got.NetworkAvailable)
	assert.Empty(t, got.NetworkReason)
}

// On a machine with no sandbox the service syncs nothing and keeps no fault,
// and a sync that fails at Start is a warning, not a startup error.
func TestASecurityServiceWithNoSandboxSyncsNothing(t *testing.T) {
	store, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	svc := newSecurityService(store, nil, nil, sandbox.Status{}, nil, "timeout", "")
	assert.Empty(t, svc.PathFault())
	assert.False(t, svc.Get().RunPath().Resolved)

	log := testutil.CaptureLogs(t)
	assert.False(t, syncPath(t.Context(), svc, []string{t.TempDir()}), "nothing changed")
	assert.Contains(t, log.String(), "PATH not synced")
	assert.Empty(t, store.Get().Path)
}
