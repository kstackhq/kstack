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

package kubeproxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

func classifyOf(method, target, contentType, body string) permissions.Action {
	r := httptest.NewRequest(method, "http://"+Host+target, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return classify(r, parsePath(r.URL.Path), []byte(body), "dev-eks")
}

const (
	jsonType     = "application/json"
	mergeType    = "application/merge-patch+json"
	strategic    = "application/strategic-merge-patch+json"
	jsonPatch    = "application/json-patch+json"
	applyPatch   = "application/apply-patch+yaml"
	deployPath   = "/apis/apps/v1/namespaces/team-a/deployments/api"
	scalePath    = "/apis/apps/v1/namespaces/team-a/deployments/api/scale"
	rcPath       = "/api/v1/namespaces/team-a/replicationcontrollers/rc"
	replicasZero = `{"spec":{"replicas":0}}`
)

func TestEveryRequestIsClassified(t *testing.T) {
	for _, c := range []struct {
		name                     string
		method, target, ct, body string
		class                    permissions.Class
		verb, group, kind        string
	}{
		{"a read", "GET", "/api/v1/namespaces/team-a/pods/api", "", "", permissions.ReadInside, "get", "core", "pods"},
		{"discovery", "GET", "/apis/apps/v1", "", "", permissions.ReadInside, "get", "apps", ""},
		{"a log", "GET", "/api/v1/namespaces/team-a/pods/api/log", "", "", permissions.ReadInside, "get", "core", "pods/log"},
		{"a secret read", "GET", "/api/v1/namespaces/team-a/secrets/x", "", "", permissions.SecretRead, "get", "core", "secrets"},
		{"a secret list", "GET", "/api/v1/secrets", "", "", permissions.SecretRead, "list", "core", "secrets"},
		{"a self review", "POST", "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", jsonType, `{}`, permissions.ReadInside, "create", "authorization.k8s.io", "selfsubjectaccessreviews"},
		{"a dry run on a deployment", "PATCH", deployPath + "?dryRun=All", mergeType, replicasZero, permissions.ReadInside, "patch", "apps", "deployments"},
		{"a dry run on a secret", "POST", "/api/v1/namespaces/team-a/secrets?dryRun=All", jsonType, `{}`, permissions.ReadInside, "create", "core", "secrets"},
		{"a dry run on a custom group", "POST", "/apis/example.com/v1/namespaces/team-a/widgets?dryRun=All", jsonType, `{}`, permissions.UpstreamWrite, "create", "example.com", "widgets"},
		{"a dry run on an aggregated group", "POST", "/apis/metrics.k8s.io/v1beta1/namespaces/team-a/pods?dryRun=All", jsonType, `{}`, permissions.UpstreamWrite, "create", "metrics.k8s.io", "pods"},
		{"a dry run on an unknown version of a built-in group", "PATCH", "/apis/apps/v9/namespaces/team-a/deployments/api?dryRun=All", mergeType, `{}`, permissions.UpstreamWrite, "patch", "apps", "deployments"},
		{"a create", "POST", "/api/v1/namespaces/team-a/pods", jsonType, `{}`, permissions.UpstreamWrite, "create", "core", "pods"},
		{"an update", "PUT", "/api/v1/namespaces/team-a/configmaps/x", jsonType, `{}`, permissions.UpstreamWrite, "update", "core", "configmaps"},
		{"a delete", "DELETE", "/api/v1/namespaces/team-a/pods/api", "", "", permissions.UpstreamWrite, "delete", "core", "pods"},
		{"a dry-run delete", "DELETE", "/api/v1/namespaces/team-a/pods/api?dryRun=All", "", "", permissions.UpstreamWrite, "delete", "core", "pods"},
		{"a delete with no name", "DELETE", "/api/v1/namespaces/team-a/pods", "", "", permissions.Destructive, "deletecollection", "core", "pods"},
		{"a namespace delete", "DELETE", "/api/v1/namespaces/team-a", "", "", permissions.Destructive, "delete", "core", "namespaces"},
		{"a node delete", "DELETE", "/api/v1/nodes/n1", "", "", permissions.Destructive, "delete", "core", "nodes"},
		{"a pv delete", "DELETE", "/api/v1/persistentvolumes/pv", "", "", permissions.Destructive, "delete", "core", "persistentvolumes"},
		{"a pvc delete", "DELETE", "/api/v1/namespaces/team-a/persistentvolumeclaims/c", "", "", permissions.Destructive, "delete", "core", "persistentvolumeclaims"},
		{"a crd delete", "DELETE", "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/w.example.com", "", "", permissions.Destructive, "delete", "apiextensions.k8s.io", "customresourcedefinitions"},
		{"a namespace finalize", "PUT", "/api/v1/namespaces/team-a/finalize", jsonType, `{}`, permissions.Destructive, "update", "core", "namespaces/finalize"},
		{"a namespace status", "PUT", "/api/v1/namespaces/team-a/status", jsonType, `{}`, permissions.UpstreamWrite, "update", "core", "namespaces/status"},
		{"a custom namespaces delete", "DELETE", "/apis/example.com/v1/namespaces/team-a/namespaces/x", "", "", permissions.UpstreamWrite, "delete", "example.com", "namespaces"},
		{"an rbac write", "POST", "/apis/rbac.authorization.k8s.io/v1/namespaces/team-a/rolebindings", jsonType, `{}`, permissions.Destructive, "create", "rbac.authorization.k8s.io", "rolebindings"},
		{"a cluster role patch", "PATCH", "/apis/rbac.authorization.k8s.io/v1/clusterroles/x", mergeType, `{}`, permissions.Destructive, "patch", "rbac.authorization.k8s.io", "clusterroles"},
		{"a webhook write", "POST", "/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations", jsonType, `{}`, permissions.Destructive, "create", "admissionregistration.k8s.io", "mutatingwebhookconfigurations"},
		{"a mutating policy write", "POST", "/apis/admissionregistration.k8s.io/v1beta1/mutatingadmissionpolicies", jsonType, `{}`, permissions.Destructive, "create", "admissionregistration.k8s.io", "mutatingadmissionpolicies"},
		{"a mutating policy binding write", "DELETE", "/apis/admissionregistration.k8s.io/v1beta1/mutatingadmissionpolicybindings/x", "", "", permissions.Destructive, "delete", "admissionregistration.k8s.io", "mutatingadmissionpolicybindings"},
		{"a policy binding write", "DELETE", "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicybindings/x", "", "", permissions.Destructive, "delete", "admissionregistration.k8s.io", "validatingadmissionpolicybindings"},
		{"a CSR approval", "PUT", "/apis/certificates.k8s.io/v1/certificatesigningrequests/x/approval", jsonType, `{}`, permissions.Destructive, "update", "certificates.k8s.io", "certificatesigningrequests/approval"},
		{"a CSR create", "POST", "/apis/certificates.k8s.io/v1/certificatesigningrequests", jsonType, `{}`, permissions.UpstreamWrite, "create", "certificates.k8s.io", "certificatesigningrequests"},
		{"an ephemeral container", "PATCH", "/api/v1/namespaces/team-a/pods/api/ephemeralcontainers", strategic, `{}`, permissions.Destructive, "patch", "core", "pods/ephemeralcontainers"},
	} {
		t.Run(c.name, func(t *testing.T) {
			act := classifyOf(c.method, c.target, c.ct, c.body)
			assert.Equal(t, c.class, act.Class)
			assert.Equal(t, c.verb, act.Verb)
			assert.Equal(t, c.group, act.Group)
			assert.Equal(t, c.kind, act.Kind)
			assert.Equal(t, "dev-eks", act.Context)
		})
	}
	act := classifyOf("DELETE", "/api/v1/namespaces/team-a/pods/api", "", "")
	assert.Equal(t, "team-a", act.Namespace)
	assert.Equal(t, "api", act.Name)

	for _, target := range []string{"/api/v1/namespaces/payments", "/api/v1/namespaces/payments/status"} {
		act = classifyOf("PATCH", target, mergeType, `{}`)
		assert.Equal(t, "payments", act.Namespace, "a write to a Namespace is in that namespace's scope")
	}
	act = classifyOf("POST", "/api/v1/namespaces", jsonType, `{"metadata":{"name":"payments"}}`)
	assert.Equal(t, "payments", act.Namespace, "a create names its Namespace in the body")
	assert.Equal(t, "Create namespaces/payments on dev-eks", act.Summary)
	act = classifyOf("POST", "/api/v1/namespaces", jsonType, `{"metadata":{"generateName":"payments-"}}`)
	assert.Empty(t, act.Namespace, "a create naming none is in no namespace's scope")
}

func TestAScaleIsClassFourOnlyInAPlainForm(t *testing.T) {
	for _, c := range []struct {
		name, method, target, ct, body string
		class                          permissions.Class
	}{
		// What passes on a Scale.
		{"merge to 3", "PATCH", scalePath, mergeType, `{"spec":{"replicas":3}}`, permissions.UpstreamWrite},
		{"strategic to 3", "PATCH", scalePath, strategic, `{"spec":{"replicas":3}}`, permissions.UpstreamWrite},
		{"put to 3", "PUT", scalePath, jsonType, `{"metadata":{"name":"api"},"spec":{"replicas":3}}`, permissions.UpstreamWrite},
		{"json patch to 3", "PATCH", scalePath, jsonPatch, `[{"op":"replace","path":"/spec/replicas","value":3}]`, permissions.UpstreamWrite},
		{"json patch add 1", "PATCH", scalePath, jsonPatch, `[{"op":"add","path":"/spec/replicas","value":1}]`, permissions.UpstreamWrite},

		// What asks on a Scale.
		{"merge to 0", "PATCH", scalePath, mergeType, replicasZero, permissions.Destructive},
		{"put to 0", "PUT", scalePath, jsonType, replicasZero, permissions.Destructive},
		{"merge without replicas", "PATCH", scalePath, mergeType, `{"metadata":{"labels":{"a":"b"}}}`, permissions.Destructive},
		{"merge spec null", "PATCH", scalePath, mergeType, `{"spec":null}`, permissions.Destructive},
		{"merge replicas null", "PATCH", scalePath, mergeType, `{"spec":{"replicas":null}}`, permissions.Destructive},
		{"replicas a string", "PATCH", scalePath, mergeType, `{"spec":{"replicas":"3"}}`, permissions.Destructive},
		{"replicas a fraction", "PATCH", scalePath, mergeType, `{"spec":{"replicas":2.5}}`, permissions.Destructive},
		{"put an empty spec", "PUT", scalePath, jsonType, `{"spec":{}}`, permissions.Destructive},
		{"apply to 3", "PATCH", scalePath, applyPatch, "spec:\n  replicas: 3\n", permissions.Destructive},
		{"strategic replace keeping 3", "PATCH", scalePath, strategic, `{"spec":{"$patch":"replace","replicas":3}}`, permissions.Destructive},
		{"strategic delete beside 3", "PATCH", scalePath, strategic, `{"spec":{"$patch":"delete","replicas":3}}`, permissions.Destructive},
		{"strategic retain keys", "PATCH", scalePath, strategic, `{"spec":{"$retainKeys":["replicas"],"replicas":3}}`, permissions.Destructive},
		{"directive on the object", "PATCH", scalePath, strategic, `{"$patch":"replace","spec":{"replicas":3}}`, permissions.Destructive},
		{"does not decode", "PATCH", scalePath, mergeType, `{"spec":`, permissions.Destructive},
		{"json patch to 0", "PATCH", scalePath, jsonPatch, `[{"op":"replace","path":"/spec/replicas","value":0}]`, permissions.Destructive},
		{"json patch remove", "PATCH", scalePath, jsonPatch, `[{"op":"remove","path":"/spec/replicas"}]`, permissions.Destructive},
		{"json patch replace spec", "PATCH", scalePath, jsonPatch, `[{"op":"replace","path":"/spec","value":{"replicas":3}}]`, permissions.Destructive},
		{"json patch move away", "PATCH", scalePath, jsonPatch, `[{"op":"move","from":"/spec/replicas","path":"/status/replicas"}]`, permissions.Destructive},
		{"json patch copy away", "PATCH", scalePath, jsonPatch, `[{"op":"copy","from":"/spec/replicas","path":"/status/replicas"}]`, permissions.Destructive},
		{"json patch elsewhere", "PATCH", scalePath, jsonPatch, `[{"op":"replace","path":"/metadata/labels/a","value":"b"}]`, permissions.Destructive},
		{"json patch key in another case", "PATCH", scalePath, jsonPatch, `[{"op":"replace","path":"/spec/replicas","Path":"/metadata/labels","value":0}]`, permissions.Destructive},
		{"json patch op in another case", "PATCH", scalePath, jsonPatch, `[{"op":"replace","Op":"test","path":"/spec/replicas","value":0}]`, permissions.Destructive},
		{"json patch does not decode", "PATCH", scalePath, jsonPatch, `[{"op":`, permissions.Destructive},

		// What passes on a workload, which leaves replicas alone when it omits them.
		{"workload merge to 3", "PATCH", deployPath, mergeType, `{"spec":{"replicas":3}}`, permissions.UpstreamWrite},
		{"workload merge of labels", "PATCH", deployPath, mergeType, `{"metadata":{"labels":{"a":"b"}}}`, permissions.UpstreamWrite},
		{"workload set image", "PATCH", deployPath, strategic,
			`{"spec":{"template":{"spec":{"$setElementOrder/containers":[{"name":"api"}],"containers":[{"name":"api","image":"api:2"}]}}}}`, permissions.UpstreamWrite},
		{"workload put without replicas", "PUT", deployPath, jsonType, `{"spec":{}}`, permissions.UpstreamWrite},
		{"workload apply without replicas", "PATCH", deployPath, applyPatch, "spec:\n  template: {}\n", permissions.UpstreamWrite},
		{"workload json patch of the image", "PATCH", deployPath, jsonPatch,
			`[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"api:2"}]`, permissions.UpstreamWrite},
		{"workload json patch of a label", "PATCH", deployPath, jsonPatch, `[{"op":"add","path":"/metadata/labels/a","value":"b"}]`, permissions.UpstreamWrite},

		// What asks on a workload.
		{"workload merge to 0", "PATCH", deployPath, mergeType, replicasZero, permissions.Destructive},
		{"workload strategic to 0", "PATCH", deployPath, strategic, replicasZero, permissions.Destructive},
		{"workload apply to 0", "PATCH", deployPath, applyPatch, "spec:\n  replicas: 0\n", permissions.Destructive},
		{"workload put to 0", "PUT", "/apis/apps/v1/namespaces/team-a/statefulsets/db", jsonType, replicasZero, permissions.Destructive},
		{"workload spec null", "PATCH", deployPath, mergeType, `{"spec":null}`, permissions.Destructive},
		{"workload directive on spec", "PATCH", deployPath, strategic, `{"spec":{"$patch":"delete"}}`, permissions.Destructive},
		{"workload json patch remove", "PATCH", deployPath, jsonPatch, `[{"op":"remove","path":"/spec/replicas"}]`, permissions.Destructive},
		{"workload json patch replace spec", "PATCH", deployPath, jsonPatch, `[{"op":"replace","path":"/spec","value":{}}]`, permissions.Destructive},
		{"workload json patch move from replicas", "PATCH", deployPath, jsonPatch,
			`[{"op":"move","from":"/spec/replicas","path":"/metadata/annotations/r"}]`, permissions.Destructive},
		{"workload json patch move onto replicas", "PATCH", deployPath, jsonPatch,
			`[{"op":"move","from":"/metadata/annotations/r","path":"/spec/replicas"}]`, permissions.Destructive},
		{"workload json patch move beside replicas", "PATCH", deployPath, jsonPatch,
			`[{"op":"move","from":"/metadata/annotations/r","path":"/metadata/labels/r"}]`, permissions.Destructive},
		{"workload json patch elsewhere at the top", "PATCH", deployPath, jsonPatch, `[{"op":"add","path":"/status","value":{}}]`, permissions.Destructive},
		{"workload json patch unknown op", "PATCH", deployPath, jsonPatch, `[{"op":"frobnicate","path":"/metadata/labels/a"}]`, permissions.Destructive},
		{"replication controller merge to 3", "PATCH", rcPath, mergeType, `{"spec":{"replicas":3}}`, permissions.UpstreamWrite},
		{"replication controller merge to 0", "PATCH", rcPath, mergeType, replicasZero, permissions.Destructive},
		{"replication controller put to 0", "PUT", rcPath, jsonType, replicasZero, permissions.Destructive},
		{"replication controller json patch remove", "PATCH", rcPath, jsonPatch, `[{"op":"remove","path":"/spec/replicas"}]`, permissions.Destructive},
		{"custom deployments to 0", "PATCH", "/apis/example.com/v1/namespaces/team-a/deployments/x", mergeType, replicasZero, permissions.UpstreamWrite},
	} {
		act := classifyOf(c.method, c.target, c.ct, c.body)
		assert.Equal(t, c.class, act.Class, c.name)
	}
}

func TestASummaryNamesTheTargetAsKubectlDoes(t *testing.T) {
	for want, act := range map[string]permissions.Action{
		"Delete pods/api-7f9c in team-a on dev-eks":        classifyOf("DELETE", "/api/v1/namespaces/team-a/pods/api-7f9c", "", ""),
		"Delete nodes/n1 on dev-eks":                       classifyOf("DELETE", "/api/v1/nodes/n1", "", ""),
		"Create pods in team-a on dev-eks":                 classifyOf("POST", "/api/v1/namespaces/team-a/pods", jsonType, `{}`),
		"Delete all pods in team-a on dev-eks":             classifyOf("DELETE", "/api/v1/namespaces/team-a/pods", "", ""),
		"Patch deployments/api/scale in team-a on dev-eks": classifyOf("PATCH", scalePath, mergeType, `{}`),
		"Replace widgets/w1 in team-a on dev-eks":          classifyOf("PUT", "/apis/example.com/v1/namespaces/team-a/widgets/w1", jsonType, `{}`),
		"Delete namespaces/team-a on dev-eks":              classifyOf("DELETE", "/api/v1/namespaces/team-a", "", ""),
	} {
		assert.Equal(t, want, act.Summary)
	}
	r := httptest.NewRequest("DELETE", "http://"+Host+"/api/v1/nodes/n1", nil)
	assert.Equal(t, "Delete nodes/n1", classify(r, parsePath(r.URL.Path), nil, "").Summary, "a record naming no context")
}

func TestTheDestructiveListIsInWords(t *testing.T) {
	assert.NotEmpty(t, Destructive)
	for _, line := range Destructive {
		assert.NotEmpty(t, line)
	}
}

func TestClassifyMarksADryRun(t *testing.T) {
	act := classifyOf("PATCH", "/apis/example.com/v1/namespaces/team-a/widgets/w?dryRun=All", mergeType, `{}`)
	assert.True(t, act.DryRun)
	assert.Equal(t, permissions.UpstreamWrite, act.Class, "a dry run an API may ignore stays class 4")
	assert.False(t, permissions.Grantable(permissions.Unmatched, act))

	act = classifyOf("PATCH", "/apis/example.com/v1/namespaces/team-a/widgets/w", mergeType, `{}`)
	assert.False(t, act.DryRun)
	act = classifyOf("DELETE", "/api/v1/namespaces/team-a/pods/api?dryRun=All", "", "")
	assert.False(t, act.DryRun, "a DELETE's dry run is in a body the proxy does not read")
}

func TestAClassSixVerbIsTheServers(t *testing.T) {
	for _, c := range []struct {
		target, verb, summary string
	}{
		{"/api/v1/namespaces/team-a/secrets/db-creds", "get", "Show Secret db-creds in team-a on dev-eks"},
		{"/api/v1/namespaces/team-a/secrets", "list", "Show Secret data in team-a on dev-eks"},
		{"/api/v1/namespaces/team-a/secrets?watch=1", "watch", "Watch Secret data in team-a on dev-eks"},
		{"/api/v1/watch/namespaces/team-a/secrets", "watch", "Watch Secret data in team-a on dev-eks"},
		{"/api/v1/watch/namespaces/team-a/secrets/db-creds", "watch", "Watch Secret db-creds in team-a on dev-eks"},
		{"/api/v1/secrets", "list", "Show Secret data on dev-eks"},
	} {
		act := classifyOf("GET", c.target, "", "")
		assert.Equal(t, permissions.SecretRead, act.Class, c.target)
		assert.Equal(t, c.verb, act.Verb, c.target)
		assert.Equal(t, c.summary, act.Summary, c.target)
	}
	assert.Equal(t, "update", classifyOf("PUT", "/api/v1/namespaces/team-a/secrets/x", jsonType, `{}`).Verb, "a write's verb is unchanged")
}
