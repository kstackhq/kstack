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

package githubproxy

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// verdict is what the classifier makes of a request: refused with refusal,
// or a read (class 0), or one that asks in class.
type verdict struct {
	refusal string
	class   permissions.Class
}

// A path pattern matches segment by segment: * is any one segment, and a
// trailing /... is the path itself and anything below it.

// credentialPaths are the requests whose answer is a credential.
var credentialPaths = []string{
	"/app/installations/*/access_tokens",
	"/app-manifests/*/conversions",
	"/applications/*/token/...",
	"/copilot_internal/...",
	"/repos/*/*/actions/runners/registration-token",
	"/repos/*/*/actions/runners/remove-token",
	"/repos/*/*/actions/runners/generate-jitconfig",
	"/orgs/*/actions/runners/registration-token",
	"/orgs/*/actions/runners/remove-token",
	"/orgs/*/actions/runners/generate-jitconfig",
	"/enterprises/*/actions/runners/registration-token",
	"/enterprises/*/actions/runners/remove-token",
	"/enterprises/*/actions/runners/generate-jitconfig",
}

// dataPaths are the reads that ask: those whose answer is a signed link to
// stored data, and the metadata GitHub documents as able to hold a secret.
var dataPaths = []string{
	"/repos/*/*/actions/runs/*/logs",
	"/repos/*/*/actions/runs/*/attempts/*/logs",
	"/repos/*/*/actions/jobs/*/logs",
	"/repos/*/*/actions/artifacts/*/*",
	"/repos/*/*/tarball/...",
	"/repos/*/*/zipball/...",
	"/orgs/*/migrations/*/archive",
	"/user/migrations/*/archive",
	"/repos/*/*/secret-scanning/...",
	"/orgs/*/secret-scanning/...",
	"/enterprises/*/secret-scanning/...",
	"/repos/*/*/hooks/...",
	"/orgs/*/hooks/...",
	"/app/hook/config/...",
	"/app/hook/deliveries/...",
	"/repos/*/*/actions/variables/...",
	"/repos/*/*/actions/organization-variables/...",
	"/orgs/*/actions/variables/...",
	"/repos/*/*/environments/*/variables/...",
}

// releaseAsset is a release asset, a data read when Accept asks for its bytes.
const releaseAsset = "/repos/*/*/releases/assets/*"

// destructivePaths are the deletes that are class 5.
var destructivePaths = []string{"/repos/*/*", "/repos/*/*/git/refs/..."}

// classifyAPI is how the API host takes a request to path other than
// GraphQL's, by its method and, for a release asset, its Accept.
func classifyAPI(method, path, accept string) verdict {
	segs := segmentsOf(path)
	if matchesAny(credentialPaths, segs) {
		return verdict{refusal: refusedCredential}
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		if matchesAny(dataPaths, segs) || matches(releaseAsset, segs) && strings.Contains(strings.ToLower(accept), "application/octet-stream") {
			return verdict{class: permissions.SecretRead}
		}
		return verdict{}
	case http.MethodDelete:
		if matchesAny(destructivePaths, segs) {
			return verdict{class: permissions.Destructive}
		}
		return verdict{class: permissions.UpstreamWrite}
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return verdict{class: permissions.UpstreamWrite}
	}
	return verdict{refusal: refusedMethod}
}

// segmentsOf is path's segments as the lists read them: in lower case, so no
// case variant of a route escapes them. GitHub serves every
// /repos/{owner}/{repo} route as /repositories/{id} too, and every
// /orgs/{org} route as /organizations/{id}, so those read as their form by
// name.
func segmentsOf(path string) []string {
	segs := strings.Split(strings.ToLower(strings.TrimPrefix(path, "/")), "/")
	if len(segs) >= 2 {
		switch segs[0] {
		case "repositories":
			return slices.Concat([]string{"repos", segs[1], segs[1]}, segs[2:])
		case "organizations":
			return slices.Concat([]string{"orgs", segs[1]}, segs[2:])
		}
	}
	return segs
}

func matchesAny(patterns []string, segs []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return matches(p, segs) })
}

// matches is whether segs is pattern's path.
func matches(pattern string, segs []string) bool {
	want := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	below := want[len(want)-1] == "..."
	if below {
		want = want[:len(want)-1]
		if len(segs) < len(want) {
			return false
		}
		segs = segs[:len(want)]
	}
	if len(segs) != len(want) {
		return false
	}
	for i, w := range want {
		if w != "*" && w != segs[i] {
			return false
		}
	}
	return true
}

// gitService is one of Git's smart HTTP requests.
type gitService int

const (
	uploadPackRefs  gitService = iota // GET info/refs?service=git-upload-pack
	uploadPack                        // POST git-upload-pack
	receivePackRefs                   // GET info/refs?service=git-receive-pack
	receivePack                       // POST git-receive-pack
)

// gitPath is a smart HTTP request's path: the owner, the repository with
// any .git, and the request.
var gitPath = regexp.MustCompile(`^/([A-Za-z0-9-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/(info/refs|git-upload-pack|git-receive-pack)$`)

// classifyGit is the repository, as owner/repo, and the service of a request
// to the Git host, if it is one of Git's smart HTTP requests.
func classifyGit(method, path, query string) (repo string, svc gitService, ok bool) {
	m := gitPath.FindStringSubmatch(path)
	if m == nil {
		return "", 0, false
	}
	repo = m[1] + "/" + m[2]
	switch {
	case m[3] == "info/refs" && method == http.MethodGet && query == "service=git-upload-pack":
		return repo, uploadPackRefs, true
	case m[3] == "info/refs" && method == http.MethodGet && query == "service=git-receive-pack":
		return repo, receivePackRefs, true
	case m[3] == "git-upload-pack" && method == http.MethodPost && query == "":
		return repo, uploadPack, true
	case m[3] == "git-receive-pack" && method == http.MethodPost && query == "":
		return repo, receivePack, true
	}
	return "", 0, false
}
