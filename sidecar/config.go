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
	"flag"
	"os"
	"path/filepath"

	"github.com/kstackhq/kstack/sidecar/internal/agent/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/app"
	"github.com/kstackhq/kstack/sidecar/internal/lib/ipc"
	"github.com/kstackhq/kstack/sidecar/internal/lib/safe"
)

// config is everything the command line decides: the app's own configuration
// plus the two values main needs to bind the listener.
type config struct {
	App    app.Config
	Socket string
	// Zero leaves the endpoint open to any process of this user, which is what
	// a standalone dev run wants; the host always passes its own.
	HostPID int
	// Where records go. Empty and false is stderr alone, the standalone default.
	LogFile   string
	LogStderr bool
}

// configFromArgs parses the sidecar's command line. Endpoints are arguments,
// never environment: the sidecar inherits the host's environment, and anything
// that can set a variable there could otherwise redirect sign-in. Only a build
// tagged `debug` lets applyEnvOverrides put the environment back in play. The
// model-provider keys are the stated exception, read here by readProviders.
// getenv is the environment those reads see — os.Getenv from main, and a test's
// own map, so a test never reads a key out of the shell running it.
//
// On a bad flag the flag package has already written the problem and the usage
// to stderr; the caller only needs to exit.
func configFromArgs(args []string, getenv func(string) string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("kstack-sidecar", flag.ContinueOnError)
	fs.StringVar(&cfg.Socket, "socket", ipc.DefaultSocketPath(), "path to the IPC endpoint (Unix domain socket on Unix, named pipe on Windows) to listen on")
	fs.IntVar(&cfg.HostPID, "host-pid", 0, "pid of the host process; the only process allowed to connect (0 allows any process of this user)")
	fs.StringVar(&cfg.App.KubeconfigPath, "kubeconfig", "", "explicit kubeconfig path; empty uses the clientcmd default-loading rules ($KUBECONFIG / ~/.kube/config)")
	fs.StringVar(&cfg.App.DataDir, "data-dir", "", "directory for what a user would lose: app.db, the chats' files (required)")
	fs.StringVar(&cfg.App.CacheDir, "cache-dir", "", "directory for what Kstack rebuilds: the mirror, the kubectl cache (required)")
	fs.StringVar(&cfg.App.RuntimeDir, "runtime-dir", "", "directory for what lives for a session: the shell snapshot, a sandboxed run's files (required)")
	// The OAuth client is public (PKCE/loopback, no secret), so baking the
	// production defaults into the binary leaks nothing.
	fs.StringVar(&cfg.App.OAuthIssuerURL, "oauth-issuer", "https://oauth.kstack.sh", "OAuth issuer URL")
	fs.StringVar(&cfg.App.OAuthClientID, "oauth-client-id", "kstack-desktop", "OAuth client id")
	// The host passes its install's name, so a dev run and an installed
	// release never share one keychain entry.
	fs.StringVar(&cfg.App.KeychainService, "keychain-service", "", "keyring service name; empty is the Kstack default")
	fs.StringVar(&cfg.LogFile, "log-file", "", "append log records to this file, rotating it by size; empty logs to stderr")
	fs.BoolVar(&cfg.LogStderr, "log-stderr", false, "also write every record to stderr")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg.App.HostPID = cfg.HostPID
	if cfg.LogFile != "" {
		cfg.App.LogDir = filepath.Dir(cfg.LogFile)
	}
	readProviders(&cfg, getenv)
	cfg.App.AddFake = debugBuild
	applyEnvOverrides(&cfg, getenv)
	return cfg, nil
}

// readProviders takes the model-provider keys from the environment in every build —
// an exception to *the environment reaches the config only in a debug build*, and
// stated as one: a key selects an account, never an endpoint, so it cannot
// redirect where anything is sent. A key that is set is kept under its provider's
// id; one that is not is left out, and the llm service lists no row for it. Each
// key is registered with the redactor here, before the logger exists, so no log
// line can carry it.
func readProviders(cfg *config, getenv func(string) string) {
	for id, keyVar := range catalog.KeyVars() {
		key := getenv(keyVar)
		if key == "" {
			continue
		}
		safe.AddSecret(key)
		if cfg.App.LLMKeys == nil {
			cfg.App.LLMKeys = map[string]string{}
		}
		cfg.App.LLMKeys[id] = key
	}
}

// takeProviderKeys removes every key variable in the table from the process
// environment, the config having read them. The sidecar spawns kubeconfig
// credential plugins and a child inherits the environment; a key left in it would
// be handed to every plugin the kubeconfig names. Only the key variables are
// cleared: the rest of the user's environment is theirs.
func takeProviderKeys() {
	for _, keyVar := range catalog.KeyVars() {
		os.Unsetenv(keyVar)
	}
}

// applyEnvOverrides lets a standalone dev run — no host to pass arguments —
// point the sidecar somewhere else. It is a no-op unless the binary was built
// with `-tags debug` (`make sidecar-dev`); the body stays compiled in every
// build so vet and the tests always see it.
func applyEnvOverrides(cfg *config, getenv func(string) string) {
	if !debugBuild {
		return
	}
	set := func(dst *string, name string) {
		if v := getenv(name); v != "" {
			*dst = v
		}
	}
	set(&cfg.App.OAuthIssuerURL, "KSTACK_OAUTH_ISSUER")
	set(&cfg.App.OAuthClientID, "KSTACK_OAUTH_CLIENT_ID")
	set(&cfg.App.DataDir, "KSTACK_DATA_DIR")
	set(&cfg.App.CacheDir, "KSTACK_CACHE_DIR")
	set(&cfg.App.RuntimeDir, "KSTACK_RUNTIME_DIR")
	set(&cfg.App.KeychainService, "KSTACK_KEYCHAIN_SERVICE")
	for id, name := range catalog.BaseURLVars() {
		url := getenv(name)
		if url == "" {
			continue
		}
		if cfg.App.LLMBaseURLs == nil {
			cfg.App.LLMBaseURLs = map[string]string{}
		}
		cfg.App.LLMBaseURLs[id] = url
	}
}
