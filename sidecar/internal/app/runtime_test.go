package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/agent/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/lifecycle"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate/sqlitemigratetest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/aws"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/github"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/google"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors/openrouter"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// The app keeps every service it built, so a caller reaches one through the
// runtime rather than through a constructor's arguments.
func TestNewKeepsTheRuntime(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{DataDir: t.TempDir()}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })

	rt := a.rt
	for name, svc := range map[string]any{
		"DB": rt.DB, "Poke": rt.Poke, "Kubeconfig": rt.Kubeconfig, "Cluster": rt.Cluster,
		"KstackCloud": rt.KstackCloud, "LLM": rt.LLM, "Security": rt.Settings,
		"GitHub": rt.GitHub,
		"Memory": rt.Memory, "Chat": rt.Chat, "ChatGPT": rt.ChatGPT, "OpenRouter": rt.OpenRouter,
		"AWS": rt.AWS, "Google": rt.Google,
	} {
		assert.NotNil(t, svc, name)
	}
}

// A runtime is built apart from the servers over it, and closes what it built
// on its own. SQLite deletes the -wal beside a database when its last connection
// closes, so the sibling's absence shows app.db was released.
func TestARuntimeClosesWhatItBuilt(t *testing.T) {
	dir := t.TempDir()
	rt, err := build(t.Context(), withDirs(t, Config{DataDir: dir}))
	require.NoError(t, err)

	wal := filepath.Join(dir, "app.db-wal")
	require.FileExists(t, wal)
	require.NoError(t, rt.Close())
	assert.NoFileExists(t, wal)
}

// An app.db that cannot open fails the build, saying so.
func TestAnAppDBThatCannotOpenFailsTheBuild(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "app.db"), 0o700))

	_, err := build(t.Context(), withDirs(t, Config{DataDir: dir}))

	assert.ErrorContains(t, err, "open app database")
}

// An app.db a newer build migrated fails the build with the text the start screen
// shows: the file and both versions.
func TestANewerAppDBFailsTheBuildNamingTheVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, err := appdb.Open(path, 0)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	sqlitemigratetest.StampNewer(t, path)

	_, err = build(t.Context(), withDirs(t, Config{DataDir: dir}))

	assert.EqualError(t, err, "app.db was written by a newer Kstack (schema 999; this build reads up to 3)")
	requireUnlocked(t, dir)
}

// With no shell the resolver has no prober. The comparison is to a nil
// interface: a nil *bash.Tool stored in one is not nil, and the resolver would
// call through it.
func TestTheResolverHasNoProberWithoutAShell(t *testing.T) {
	rt := &Runtime{SandboxStatus: sandbox.Status{Available: true}}

	assert.True(t, rt.resolver().Executables == nil)
}

// The resolver and the chat service list the build's cloud platforms in its
// order.
func TestCloudTargetsKeepTheBuildsOrder(t *testing.T) {
	got := cloudTargets([]bash.CloudPlatform{targetOnly(permissions.GCP), targetOnly(permissions.AWS)})
	assert.Equal(t, []permissions.Target{permissions.GCP, permissions.AWS}, got)
	assert.Empty(t, cloudTargets(nil))
}

// With a sandbox the cloud platforms are the build's, or a test's in their
// place; without one there are none.
func TestCloudPlatformsNeedASandbox(t *testing.T) {
	sandboxed := sandbox.Status{Available: true}
	configured := []bash.CloudPlatform{targetOnly(permissions.AWS)}
	assert.Equal(t, cloudTargets(builtCloudPlatforms(nil, nil)), cloudTargets(cloudPlatformsFor(sandboxed, nil, nil, nil)))
	assert.Equal(t, []permissions.Target{permissions.AWS}, cloudTargets(cloudPlatformsFor(sandboxed, configured, nil, nil)))
	assert.Empty(t, cloudPlatformsFor(sandbox.Status{Reason: "no bwrap"}, nil, nil, nil))
	assert.Empty(t, cloudPlatformsFor(sandbox.Status{}, configured, nil, nil))
}

// targetOnly is a cloud platform that names its target and mounts nothing.
type targetOnly permissions.Target

func (p targetOnly) Target() permissions.Target { return permissions.Target(p) }

func (targetOnly) Mount(context.Context, bash.CloudRun) (bash.CloudMount, error) {
	return bash.CloudMount{}, nil
}

// Every keyed provider is in llm.Service from launch; one with no credential lists
// no model, a save lists its written catalog on modelsWatch and a delete
// empties it.
func TestAKeyedRowListsItsModelsWhileItHasACredential(t *testing.T) {
	rt, err := build(t.Context(), withDirs(t, Config{}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rt.Close()) })
	stop, err := lifecycle.StartAll(t.Context(), rt.parts)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stop(context.Background())) })
	for id := range catalog.KeyVars() {
		_, ok := rt.LLM.Provider(id)
		assert.True(t, ok, "%s is listed from launch", id)
	}

	frames, cancel := rt.LLM.Subscribe()
	defer cancel()
	listed := func() bool {
		p, _ := rt.LLM.Provider("groq")
		return len(p.Catalog) > 0
	}
	assert.False(t, listed(), "no credential, no model")
	require.NoError(t, rt.ProviderKeys.Save("groq", "gsk-saved-0123456789abcdef"))
	for !listed() {
		testutil.Recv(t, frames, "groq's models")
	}
	require.NoError(t, rt.ProviderKeys.Delete("groq"))
	for listed() {
		testutil.Recv(t, frames, "groq's models leave")
	}
}

// With OPENROUTER_API_KEY set the connector still connects and disconnects,
// and the provider signs with the saved key, then the environment key.
func TestOpenRouterFallsBackAfterADisconnect(t *testing.T) {
	const envKey, savedKey = "sk-or-env-0123456789abcdef", "sk-or-saved-0123456789abcdef"
	rt, err := build(t.Context(), withDirs(t, Config{LLMKeys: map[string]string{openrouter.ID: envKey}}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rt.Close()) })
	require.NoError(t, rt.OpenRouter.Disconnect(), "the environment key no longer refuses it")

	p, ok := rt.LLM.Provider(openrouter.ID)
	require.True(t, ok)
	assert.Equal(t, rt.ProviderKeys.Signer(openrouter.ID), p.Signer)
	assert.Empty(t, p.Key)
	assert.NotEmpty(t, p.Catalog)

	bearer := func() string {
		t.Helper()
		target, err := rt.LLM.Resolve(openrouter.ID, p.Catalog[0].ID, p.Catalog[0].DefaultEffort)
		require.NoError(t, err)
		r, err := http.NewRequest(http.MethodPost, openrouter.APIBaseURL+"/chat/completions", nil)
		require.NoError(t, err)
		require.NoError(t, target.Provider.Signer.Sign(r))
		return r.Header.Get("Authorization")
	}
	assert.Equal(t, "Bearer "+envKey, bearer())
	require.NoError(t, rt.ProviderKeys.Save(openrouter.ID, savedKey))
	assert.Equal(t, "Bearer "+savedKey, bearer())
	require.NoError(t, rt.ProviderKeys.Delete(openrouter.ID))
	assert.Equal(t, "Bearer "+envKey, bearer())
}

// The Google connector signs the google row at the project's Vertex AI
// endpoint whenever the launch found a project, a Gemini key or not.
func TestTheGoogleRowFollowsTheProject(t *testing.T) {
	vertexBase := google.BaseURL("my-project", "global")
	rows := func(cfg Config) (*Runtime, llm.Provider, bool) {
		t.Helper()
		rt, err := build(t.Context(), withDirs(t, cfg))
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, rt.Close()) })
		p, ok := rt.LLM.Provider(google.ID)
		return rt, p, ok
	}

	rt, p, ok := rows(Config{Getenv: googleEnv("my-project")})
	require.True(t, ok)
	assert.Equal(t, rt.Google, p.Signer)
	assert.Equal(t, vertexBase, p.BaseURL)
	assert.Equal(t, "my-project", rt.Google.Project())

	_, _, ok = rows(Config{})
	assert.False(t, ok, "no project")

	_, _, ok = rows(Config{Getenv: googleEnv("my-project"), LLMKeys: map[string]string{"gemini": "gm-0123456789abcdef"}})
	assert.True(t, ok, "a Gemini key does not hide Vertex AI")
}

// A debug build's OpenRouter base that does not parse fails the build.
func TestABadOpenRouterBaseFailsTheBuild(t *testing.T) {
	_, err := build(t.Context(), withDirs(t, Config{LLMBaseURLs: map[string]string{openrouter.ID: "http://[::1"}}))

	assert.ErrorContains(t, err, "http://[::1")
}

// A debug build's AWS sign-in URL that does not parse fails the build.
func TestABadAWSSigninURLFailsTheBuild(t *testing.T) {
	_, err := build(t.Context(), withDirs(t, Config{ConnectorAuthURLs: map[string]string{aws.ID: "http://[::1"}}))

	assert.ErrorContains(t, err, "http://[::1")
}

// A debug build's GitHub origin that does not parse fails the build.
func TestABadGitHubAuthURLFailsTheBuild(t *testing.T) {
	_, err := build(t.Context(), withDirs(t, Config{ConnectorAuthURLs: map[string]string{github.ID: "http://[::1"}}))

	assert.ErrorContains(t, err, "http://[::1")
}

// The wire reaches the app's OpenRouter connector, whatever key the
// environment holds, and the provider keys service.
func TestTheWireReachesTheOpenRouterConnector(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{LLMKeys: map[string]string{openrouter.ID: "sk-or-env-0123456789abcdef"}}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	ts := serveApp(t, a)

	assert.JSONEq(t, `{"data":{"openRouterConnection":{"phase":"SignedOut"}}}`, graphql(t, ts.URL, `{ openRouterConnection { phase } }`))
	assert.Contains(t, graphql(t, ts.URL, `{ providerCredentials { id source } }`), `{"id":"openrouter","source":"EnvironmentKey"}`)
}

// The wire reaches the app's AWS connector.
func TestTheWireReachesTheAWSConnector(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	ts := serveApp(t, a)

	assert.JSONEq(t, `{"data":{"awsConnection":{"phase":"SignedOut"}}}`, graphql(t, ts.URL, `{ awsConnection { phase } }`))
}

// The wire reaches the app's GitHub connector.
func TestTheWireReachesTheGitHubConnector(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	ts := serveApp(t, a)

	assert.JSONEq(t, `{"data":{"gitHubConnection":{"phase":"SignedOut"}}}`, graphql(t, ts.URL, `{ gitHubConnection { phase } }`))
}

// The wire reaches the app's Google connector, whose catalog starts with the
// app.
func TestTheWireReachesTheGoogleConnector(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{Getenv: googleEnv("my-project")}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	require.GreaterOrEqual(t, partIndex(t, a, "google catalog"), 0)
	startApp(t, a)
	ts := serveApp(t, a)

	assert.JSONEq(t, `{"data":{"googleConnection":{"phase":"SignedOut","project":"my-project"}}}`, graphql(t, ts.URL, `{ googleConnection { phase project } }`))
}

// googleEnv is an environment naming project as GOOGLE_CLOUD_PROJECT.
func googleEnv(project string) func(string) string {
	return func(name string) string {
		if name == "GOOGLE_CLOUD_PROJECT" {
			return project
		}
		return ""
	}
}

// The project is read through Getenv as Google's own tools read it, and one
// that builds no endpoint is none, so the row says to set one.
func TestFindGoogleProject(t *testing.T) {
	env := map[string]string{"GOOGLE_CLOUD_PROJECT": "my-project", "GOOGLE_CLOUD_LOCATION": "us-east5"}
	project, base := findGoogleProject(func(name string) string { return env[name] })
	assert.Equal(t, "my-project", project)
	assert.Equal(t, "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/endpoints/openapi", base)

	project, base = findGoogleProject(googleEnv("My-Project"))
	assert.Empty(t, project, "malformed")
	assert.Empty(t, base, "malformed")

	project, base = findGoogleProject(nil)
	assert.Empty(t, project, "no Getenv")
	assert.Empty(t, base, "no Getenv")
}

// A debug build's keyed base with no host fails the build, naming the provider.
func TestABadKeyedBaseFailsTheBuild(t *testing.T) {
	_, err := build(t.Context(), withDirs(t, Config{LLMBaseURLs: map[string]string{"groq": "localhost:8080"}}))

	assert.ErrorContains(t, err, "groq")
}
