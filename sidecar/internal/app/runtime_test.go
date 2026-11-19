package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/sqlitemigrate/sqlitemigratetest"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/connectors"
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

// With no OPENROUTER_API_KEY the connector signs the openrouter row; with one,
// the key does, and the connector says so.
func TestTheOpenRouterRowFollowsTheEnvironmentKey(t *testing.T) {
	row := func(rt *Runtime) llm.Provider {
		t.Helper()
		p, ok := rt.LLM.Provider(openrouter.ID)
		require.True(t, ok)
		return p
	}

	rt, err := build(t.Context(), withDirs(t, Config{}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rt.Close()) })
	assert.NotEqual(t, connectors.FromEnvironment, rt.OpenRouter.State().Phase)
	assert.Equal(t, rt.OpenRouter, row(rt).Signer)
	assert.Empty(t, row(rt).Key)

	keyed, err := build(t.Context(), withDirs(t, Config{LLMKeys: map[string]string{openrouter.ID: "sk-or-env"}}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, keyed.Close()) })
	assert.Equal(t, connectors.FromEnvironment, keyed.OpenRouter.State().Phase)
	assert.Equal(t, "sk-or-env", row(keyed).Key)
	assert.Nil(t, row(keyed).Signer)
}

// The Google connector signs the google row at the project's Vertex AI
// endpoint. With no project there is no row; with GEMINI_API_KEY the gemini row
// serves the models and there is none either.
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

	_, _, ok = rows(Config{Getenv: googleEnv("my-project"), LLMKeys: map[string]string{"gemini": "key"}})
	assert.False(t, ok, "GEMINI_API_KEY")
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

// The wire reaches the app's OpenRouter connector.
func TestTheWireReachesTheOpenRouterConnector(t *testing.T) {
	a, err := New(t.Context(), withDirs(t, Config{LLMKeys: map[string]string{openrouter.ID: "sk-or-env"}}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, a.Close()) })
	ts := serveApp(t, a)

	assert.JSONEq(t, `{"data":{"openRouterConnection":{"phase":"FromEnvironment"}}}`, graphql(t, ts.URL, `{ openRouterConnection { phase } }`))
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
