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

package azureproxy

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// What the classifier refuses unasked.
const (
	refusedRunCommand = "kstack: AKS run-command runs commands in the cluster around its proxy. Use kubectl."
	refusedCredential = "kstack: this Azure action answers a credential, which the sandbox never reaches."
	refusedUnnamed    = "kstack: this Azure list or get action is not known to answer no secret, so it is refused."
)

// verdict is one request's reading: a refusal, or its class, 0 for a read.
type verdict struct {
	refusal string
	class   permissions.Class
	// reads is what a class 6 read answers, in the user's words.
	reads string
}

// credentialWords are the words in an action's name that say its answer is
// a credential.
var credentialWords = []string{"key", "token", "sas", "credential", "password", "connectionstring"}

// credentialActions are the actions, by namespace, whose answer is a
// credential whatever their method: a GET is never read for credential words,
// and a name may say nothing of what it answers.
var credentialActions = map[string][]string{
	"microsoft.documentdb": {"readonlykeys"}, // a GET answering read-only master keys
	"microsoft.web":        {"publishxml"},   // a publishing profile: deployment users and passwords
}

// classify reads a request that passed the path policy. The first row that
// matches decides; a row naming a namespace reads the path's last scope.
func classify(method string, a armPath, query url.Values) verdict {
	ns, types := strings.ToLower(a.namespace()), a.types()
	final := strings.ToLower(a.final())
	aksCluster := ns == "microsoft.containerservice" && len(types) > 0 && types[0] == "managedclusters"
	switch {
	case aksCluster && (final == "runcommand" || slices.Contains(types, "commandresults")):
		return verdict{refusal: refusedRunCommand}
	case aksCluster && slices.Contains(types, "accessprofiles"),
		slices.Contains(credentialActions[ns], final):
		return verdict{refusal: refusedCredential}
	}
	if reads := secretMetadata(method, a, ns, types, query); reads != "" {
		return verdict{class: permissions.SecretRead, reads: reads}
	}
	if method == http.MethodPost {
		for _, w := range credentialWords {
			if strings.Contains(final, w) {
				return verdict{refusal: refusedCredential}
			}
		}
		if strings.HasPrefix(final, "list") || strings.HasPrefix(final, "get") {
			return verdict{refusal: refusedUnnamed}
		}
	}
	switch {
	case method == http.MethodGet || method == http.MethodHead:
		return verdict{}
	case destructive(method, a, ns, types):
		return verdict{class: permissions.Destructive}
	}
	return verdict{class: permissions.UpstreamWrite}
}

// types is the last scope's types, then its trailing segment, lower-cased.
func (a armPath) types() []string {
	var ts []string
	for _, p := range a.pairs() {
		ts = append(ts, strings.ToLower(p.typ))
	}
	if a.last != "" && len(a.scopes) > 0 {
		ts = append(ts, strings.ToLower(a.last))
	}
	return ts
}

// secretNamespaces are the namespaces whose resources the secret-metadata
// list names. An operation's result there can be the finished resource.
var secretNamespaces = []string{
	"microsoft.apimanagement", "microsoft.app", "microsoft.automation", "microsoft.containerinstance",
	"microsoft.resources", "microsoft.web",
}

// secretMetadata is what a read on the list of metadata Azure documents as
// able to hold a secret answers, or "".
func secretMetadata(method string, a armPath, ns string, types []string, query url.Values) string {
	get := method == http.MethodGet
	switch {
	case get && slices.Contains(secretNamespaces, ns) && len(types) > 0 && strings.HasSuffix(types[len(types)-1], "operationresults"):
		return "an operation's result"
	case ns == "microsoft.web" && method == http.MethodPost && webConfigList(a, types, "appsettings"):
		return "app settings"
	case ns == "microsoft.web" && method == http.MethodPost && webConfigList(a, types, "connectionstrings"):
		return "connection strings"
	case ns == "microsoft.compute" && get && expands(query, "userdata"):
		return "user data"
	case ns == "microsoft.resources" && get && slices.Equal(types, []string{"deployments"}):
		return "a deployment"
	case ns == "microsoft.resources" && method == http.MethodPost && slices.Equal(types, []string{"deployments", "exporttemplate"}):
		return "a deployment's template"
	case ns == "microsoft.automation" && get && slices.Equal(types, []string{"automationaccounts", "variables"}):
		return "Automation variables"
	case ns == "microsoft.apimanagement" && get && len(types) > 1 && types[0] == "service" && types[len(types)-1] == "subscriptions":
		return "API Management subscriptions"
	case ns == "microsoft.web" && get && slices.Contains(types, "hybridconnectionnamespaces"):
		return "a hybrid connection"
	case ns == "microsoft.app" && get && containerTemplate(types),
		ns == "microsoft.containerinstance" && get && slices.Equal(types, []string{"containergroups"}):
		return "a container's environment"
	}
	return ""
}

// webConfigList is whether a path is an app's, or a slot's,
// config/{setting}/list.
func webConfigList(a armPath, types []string, setting string) bool {
	if !slices.Equal(types, []string{"sites", "config", "list"}) && !slices.Equal(types, []string{"sites", "slots", "config", "list"}) {
		return false
	}
	ps := a.pairs()
	return is(ps[len(ps)-1].name, setting)
}

// containerTemplate is whether a Container Apps path answers a container
// template, whose environment can hold a secret: an app or its revisions, a
// job or its executions.
func containerTemplate(types []string) bool {
	for _, t := range [][]string{
		{"containerapps"}, {"containerapps", "revisions"}, {"jobs"}, {"jobs", "executions"},
	} {
		if slices.Equal(types, t) {
			return true
		}
	}
	return false
}

// expands is whether query's $expand, its name in any case as Resource
// Manager reads it, names what, in any case.
func expands(query url.Values, what string) bool {
	for key, values := range query {
		if !strings.EqualFold(key, "$expand") {
			continue
		}
		for _, v := range values {
			for item := range strings.SplitSeq(v, ",") {
				if strings.EqualFold(strings.TrimSpace(item), what) {
					return true
				}
			}
		}
	}
	return false
}

// destructive is whether a change is on the class 5 list.
func destructive(method string, a armPath, ns string, types []string) bool {
	if method == http.MethodDelete {
		switch {
		case a.group == "", a.isGroup():
			return true
		case ns == "microsoft.containerservice" && slices.Equal(types, []string{"managedclusters"}) && a.last == "":
			return true
		}
	}
	return ns == "microsoft.authorization" || slices.Contains(types, "runcommand") || slices.Contains(types, "runcommands")
}
