// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/app"
	"github.com/kstackhq/kstack/sidecar/internal/lib/ipc"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// What a bare command line yields: the production endpoints.
var productionConfig = config{
	App: app.Config{
		OAuthIssuerURL: "https://oauth.kstack.sh",
		OAuthClientID:  "kstack-desktop",
		AddFake:        debugBuild,
	},
	Socket: ipc.DefaultSocketPath(),
}

func assertConfig(t *testing.T, want, got config, msgAndArgs ...any) {
	t.Helper()
	assert.Equal(t, want, got, msgAndArgs...)
}

// env is the environment a test hands the config: what it holds and nothing
// else, so a key exported in the shell running the tests is never read.
type env map[string]string

func (e env) get(name string) string { return e[name] }

// noEnv is an environment with nothing in it.
var noEnv = env{}

func mustConfigFromArgs(t *testing.T, args []string, e env) config {
	t.Helper()
	cfg, err := configFromArgs(args, e.get)
	if err != nil {
		t.Fatalf("configFromArgs(%q): %v", args, err)
	}
	return cfg
}

func TestConfigFromArgsDefaultsToProduction(t *testing.T) {
	assertConfig(t, productionConfig, mustConfigFromArgs(t, nil, noEnv))
}

// Pins the tag boundary. `go test ./...` builds untagged, so the ordinary run
// asserts the environment never reaches the config; `go test -tags debug`
// asserts the override a `make sidecar-dev` build honours.
func TestConfigFromArgsIgnoresEnvironment(t *testing.T) {
	e := env{
		"KSTACK_OAUTH_ISSUER":     "https://issuer.override",
		"KSTACK_OAUTH_CLIENT_ID":  "override",
		"KSTACK_DATA_DIR":         "/tmp/override",
		"KSTACK_CACHE_DIR":        "/tmp/override-cache",
		"KSTACK_RUNTIME_DIR":      "/tmp/override-run",
		"KSTACK_KEYCHAIN_SERVICE": "Kstack-override",
	}

	want := productionConfig
	if debugBuild {
		want.App.OAuthIssuerURL = "https://issuer.override"
		want.App.OAuthClientID = "override"
		want.App.DataDir = "/tmp/override"
		want.App.CacheDir = "/tmp/override-cache"
		want.App.RuntimeDir = "/tmp/override-run"
		want.App.KeychainService = "Kstack-override"
	}
	assertConfig(t, want, mustConfigFromArgs(t, nil, e))
}

func TestConfigFromArgsReadsFlags(t *testing.T) {
	want := config{
		App: app.Config{
			KubeconfigPath:  "/tmp/kubeconfig",
			DataDir:         "/tmp/data",
			CacheDir:        "/tmp/cache",
			RuntimeDir:      "/tmp/run",
			OAuthIssuerURL:  "https://issuer.example",
			OAuthClientID:   "other",
			KeychainService: "Kstack-dev",
			AddFake:         debugBuild,
			HostPID:         42,
			LogDir:          filepath.Dir("/tmp/logs/sidecar.log"),
		},
		Socket:    "/tmp/sock",
		HostPID:   42,
		LogFile:   "/tmp/logs/sidecar.log",
		LogStderr: true,
	}
	cfg := mustConfigFromArgs(t, []string{
		"--socket", "/tmp/sock",
		"--host-pid", "42",
		"--kubeconfig", "/tmp/kubeconfig",
		"--data-dir", "/tmp/data",
		"--cache-dir", "/tmp/cache",
		"--runtime-dir", "/tmp/run",
		"--oauth-issuer", "https://issuer.example",
		"--oauth-client-id", "other",
		"--keychain-service", "Kstack-dev",
		"--log-file", "/tmp/logs/sidecar.log",
		"--log-stderr",
	}, noEnv)
	assertConfig(t, want, cfg)
}

// Both are off by default, which is the standalone run and the test default: stderr is the
// whole log and nothing writes a file the caller did not ask for.
func TestConfigFromArgsLogsToStderrByDefault(t *testing.T) {
	cfg := mustConfigFromArgs(t, nil, noEnv)
	if cfg.LogFile != "" || cfg.LogStderr || cfg.App.LogDir != "" {
		t.Errorf("log flags = (%q, %v, %q), want empty, false and empty", cfg.LogFile, cfg.LogStderr, cfg.App.LogDir)
	}
}

func TestConfigFromArgsRejectsAnUnknownFlag(t *testing.T) {
	if _, err := configFromArgs([]string{"--nope"}, noEnv.get); err == nil {
		t.Error("configFromArgs(--nope) = nil error, want one")
	}
}

// A set key is kept under its provider, in every build; an unset one is not. The
// config holds the key, so the environment can be cleared behind it.
func TestConfigFromArgsReadsTheProviderKeys(t *testing.T) {
	assert.Empty(t, mustConfigFromArgs(t, nil, noEnv).App.LLMKeys)

	cfg := mustConfigFromArgs(t, nil, env{"ANTHROPIC_API_KEY": "sk-ant-test-0123456789"})

	assert.Equal(t, map[string]string{"anthropic": "sk-ant-test-0123456789"}, cfg.App.LLMKeys)
}

// A provider's base URL moves under `-tags debug` alone, by the variable
// catalog.BaseURLVars names for it: the ordinary run asserts the environment
// never reaches LLMBaseURLs.
func TestConfigFromArgsMovesAProvidersBaseURLInADebugBuild(t *testing.T) {
	cfg := mustConfigFromArgs(t, nil, env{"KSTACK_ANTHROPIC_BASE_URL": "https://anthropic.override"})

	if debugBuild {
		assert.Equal(t, map[string]string{"anthropic": "https://anthropic.override"}, cfg.App.LLMBaseURLs)
	} else {
		assert.Nil(t, cfg.App.LLMBaseURLs)
	}
	assert.Nil(t, mustConfigFromArgs(t, nil, noEnv).App.LLMBaseURLs, "none set is none moved")
}

// Every key read is registered with the redactor before the logger exists.
func TestConfigFromArgsRegistersEveryKeyWithTheRedactor(t *testing.T) {
	t.Cleanup(safe.ResetSecrets)
	mustConfigFromArgs(t, nil, env{"ANTHROPIC_API_KEY": "sk-ant-test-0123456789"})

	assert.NotContains(t, safe.String("key sk-ant-test-0123456789 here"), "sk-ant-test-0123456789")
}

// The clear touches the table's variables alone: the config keeps the key it read,
// and a variable beside it survives.
func TestTakeProviderKeysClearsOnlyTheTable(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-0123456789")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:11434")
	cfg, err := configFromArgs(nil, os.Getenv)
	require.NoError(t, err)

	takeProviderKeys()

	_, set := os.LookupEnv("ANTHROPIC_API_KEY")
	assert.False(t, set, "the key is out of the environment")
	assert.Equal(t, "http://127.0.0.1:11434", os.Getenv("OLLAMA_HOST"), "another variable is left alone")
	assert.Equal(t, "sk-ant-test-0123456789", cfg.App.LLMKeys["anthropic"], "the config still holds it")
}
