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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// byID is path with its /repos/o/r or /orgs/o written by id, as GitHub
// serves it too; "" for a path with neither.
func byID(path string) string {
	switch {
	case strings.HasPrefix(path, "/repos/o/r"):
		return "/repositories/42" + strings.TrimPrefix(path, "/repos/o/r")
	case strings.HasPrefix(path, "/orgs/o"):
		return "/organizations/7" + strings.TrimPrefix(path, "/orgs/o")
	}
	return ""
}

// classified is what classifyAPI makes of method and path, with accept, in
// one word: refused, read, or the number of the class it asks in.
func classified(method, path, accept string) string {
	v := classifyAPI(method, path, accept)
	switch {
	case v.refusal != "":
		return "refused"
	case v.class == 0:
		return "read"
	}
	return strconv.Itoa(int(v.class))
}

func TestEveryRequestIsClassified(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ method, path, accept, want string }{
		{http.MethodGet, "/user", "", "read"},
		{http.MethodHead, "/repos/o/r", "", "read"},
		{http.MethodGet, "/repos/o/r/contents/README.md", "", "read"},
		{http.MethodGet, "/repos/o/r/actions/secrets", "", "read"},
		{http.MethodGet, "/repos/o/r/releases/assets/9", "application/json", "read"},
		{http.MethodGet, "/repos/o/r/releases/assets/9", "application/octet-stream", "6"},
		{http.MethodGet, "/repos/o/r/actions/runs/1/logs", "", "6"},
		{http.MethodPost, "/repos/o/r/issues", "", "4"},
		{http.MethodPut, "/repos/o/r/contents/a", "", "4"},
		{http.MethodPatch, "/repos/o/r", "", "4"},
		{http.MethodDelete, "/repos/o/r/issues/comments/1", "", "4"},
		{http.MethodDelete, "/repos/o/r", "", "5"},
		{http.MethodDelete, "/repos/o/r/git/refs/heads/main", "", "5"},
		{http.MethodOptions, "/user", "", "refused"},
		{"PROPFIND", "/user", "", "refused"},
		{http.MethodPost, "/app/installations/1/access_tokens", "", "refused"},
	} {
		assert.Equal(t, c.want, classified(c.method, c.path, c.accept), "%s %s", c.method, c.path)
	}
}

// A request names a repository or an org by id as GitHub serves it, and
// lands in the row of its form by name.
func TestAPathByIDIsClassifiedAsItsRoute(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/repos/o/r/hooks"},
		{http.MethodGet, "/repos/o/r/tarball/main"},
		{http.MethodPost, "/repos/o/r/actions/runners/registration-token"},
		{http.MethodPost, "/orgs/o/actions/runners/remove-token"},
		{http.MethodGet, "/orgs/o/actions/variables"},
		{http.MethodDelete, "/repos/o/r"},
		{http.MethodDelete, "/repos/o/r/git/refs/tags/v1"},
		{http.MethodGet, "/repos/o/r/pulls"},
	} {
		assert.Equal(t, classified(c.method, c.path, ""), classified(c.method, byID(c.path), ""), "%s %s", c.method, byID(c.path))
	}
	assert.Equal(t, "read", classified(http.MethodGet, "/repositories/42", ""))
	assert.Equal(t, "refused", classified(http.MethodPost, "/Repositories/42/actions/runners/registration-token", ""), "the alias in any case")
	assert.Equal(t, "read", classified(http.MethodGet, "/repositories", ""), "the list of every repository names none")
}

func TestACredentialPathIsRefused(t *testing.T) {
	t.Parallel()
	paths := []string{
		"/app/installations/1/access_tokens",
		"/app-manifests/abc/conversions",
		"/applications/Iv1/token",
		"/applications/Iv1/token/scoped",
		"/copilot_internal",
		"/copilot_internal/v2/token",
	}
	for _, scope := range []string{"/repos/o/r", "/orgs/o", "/enterprises/e"} {
		for _, name := range []string{"registration-token", "remove-token", "generate-jitconfig"} {
			paths = append(paths, scope+"/actions/runners/"+name)
		}
	}
	for _, path := range paths {
		for _, p := range []string{path, byID(path)} {
			if p == "" {
				continue
			}
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
				v := classifyAPI(method, p, "")
				assert.Equal(t, refusedCredential, v.refusal, "%s %s", method, p)
			}
		}
	}
	assert.Equal(t, "read", classified(http.MethodGet, "/applications/Iv1/grants", ""))
	assert.Equal(t, "read", classified(http.MethodGet, "/repos/o/r/actions/runners", ""))
}

// The lists match a path in any case, so a case variant of a route, should
// GitHub serve one, is never read unasked.
func TestAPathIsMatchedInAnyCase(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "refused", classified(http.MethodPost, "/repos/o/r/actions/runners/Registration-Token", ""))
	assert.Equal(t, "refused", classified(http.MethodGet, "/COPILOT_INTERNAL/v2/token", ""))
	assert.Equal(t, "6", classified(http.MethodGet, "/repos/o/r/Hooks/1/config", ""))
	assert.Equal(t, "6", classified(http.MethodGet, "/REPOS/o/r/actions/Variables", ""))
	assert.Equal(t, "5", classified(http.MethodDelete, "/Repos/o/r", ""))
	assert.Equal(t, "6", classified(http.MethodGet, "/repos/o/r/releases/assets/9", "Application/Octet-Stream"), "the media type in any case")
}

func TestADataPathAsks(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/repos/o/r/actions/runs/1/logs",
		"/repos/o/r/actions/runs/1/attempts/2/logs",
		"/repos/o/r/actions/jobs/3/logs",
		"/repos/o/r/actions/artifacts/4/zip",
		"/repos/o/r/tarball",
		"/repos/o/r/tarball/main",
		"/repos/o/r/zipball/v1",
		"/orgs/o/migrations/5/archive",
		"/user/migrations/6/archive",
		"/repos/o/r/secret-scanning/alerts",
		"/orgs/o/secret-scanning/alerts",
		"/enterprises/e/secret-scanning/alerts",
		"/repos/o/r/hooks",
		"/repos/o/r/hooks/1/deliveries/2",
		"/orgs/o/hooks/1/config",
		"/app/hook/config",
		"/app/hook/deliveries/3",
		"/repos/o/r/actions/variables",
		"/orgs/o/actions/variables/NAME",
		"/repos/o/r/actions/organization-variables",
		"/repos/o/r/actions/organization-variables/NAME",
		"/repos/o/r/environments/prod/variables",
	} {
		for _, p := range []string{path, byID(path)} {
			if p == "" {
				continue
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				assert.Equal(t, "6", classified(method, p, ""), "%s %s", method, p)
			}
		}
	}
	assert.Equal(t, "read", classified(http.MethodGet, "/repos/o/r/releases/assets/9", ""), "an asset's metadata")
	assert.Equal(t, "6", classified(http.MethodGet, "/repos/o/r/releases/assets/9", "application/octet-stream"))
	assert.Equal(t, "6", classified(http.MethodGet, byID("/repos/o/r/releases/assets/9"), "application/octet-stream"))
	assert.Equal(t, "read", classified(http.MethodGet, "/repos/o/r/actions/runs/1", ""), "a run's metadata")
	assert.Equal(t, "4", classified(http.MethodPost, "/repos/o/r/hooks", ""), "a change asks as a change")
}

func TestOnlyGitsSmartProtocolIsAGitRoute(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		method, path, query string
		repo                string
		svc                 gitService
	}{
		{http.MethodGet, "/o/r/info/refs", "service=git-upload-pack", "o/r", uploadPackRefs},
		{http.MethodGet, "/o/r.git/info/refs", "service=git-upload-pack", "o/r", uploadPackRefs},
		{http.MethodPost, "/o/r/git-upload-pack", "", "o/r", uploadPack},
		{http.MethodGet, "/o/r.wiki.git/info/refs", "service=git-receive-pack", "o/r.wiki", receivePackRefs},
		{http.MethodPost, "/my-org/my_repo.js/git-receive-pack", "", "my-org/my_repo.js", receivePack},
	} {
		repo, svc, ok := classifyGit(c.method, c.path, c.query)
		assert.True(t, ok, "%s %s?%s", c.method, c.path, c.query)
		assert.Equal(t, c.repo, repo)
		assert.Equal(t, c.svc, svc)
	}
	for _, c := range []struct{ method, path, query string }{
		{http.MethodGet, "/o/r/info/refs", ""},
		{http.MethodGet, "/o/r/info/refs", "service=git-upload-archive"},
		{http.MethodGet, "/o/r/info/refs", "service=git-upload-pack&x=1"},
		{http.MethodPost, "/o/r/info/refs", "service=git-upload-pack"},
		{http.MethodGet, "/o/r/git-upload-pack", ""},
		{http.MethodPost, "/o/r/git-upload-pack", "x=1"},
		{http.MethodGet, "/o/r/HEAD", ""},
		{http.MethodGet, "/o/r/objects/info/packs", ""},
		{http.MethodPost, "/o/r.git/info/lfs/objects/batch", ""},
		{http.MethodGet, "/o/r", ""},
		{http.MethodGet, "/o/r/pulls", ""},
		{http.MethodPost, "/o/git-receive-pack", ""},
		{http.MethodPost, "/o/r/x/git-receive-pack", ""},
	} {
		_, _, ok := classifyGit(c.method, c.path, c.query)
		assert.False(t, ok, "%s %s?%s", c.method, c.path, c.query)
	}
}
