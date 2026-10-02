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
	"encoding/json"
	"mime"
	"net/http"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// Destructive is the class 5 list in words, as Settings shows it under
// Always asks: one line per item destructive decides on.
var Destructive = []string{
	"Deleting a namespace, node, persistent volume, persistent volume claim or custom resource definition",
	"Deleting a whole collection in one request (kubectl deletes one object at a time, so its --all and -l are decided object by object)",
	"Any change to roles, cluster roles or their bindings",
	"Any change to admission webhooks or admission policies",
	"Approving a certificate signing request",
	"Adding an ephemeral container to a pod",
	"Scaling to zero, or any change to a scale or to a deployment's, stateful set's, replica set's or replication controller's replicas that is not a plain positive count",
}

// verbs is the Kubernetes verb of each method on a named resource.
var verbs = map[string]string{
	http.MethodGet: "get", http.MethodPost: "create", http.MethodPut: "update",
	http.MethodPatch: "patch", http.MethodDelete: "delete",
}

// classify is what a request the policy passed does, for Decide: its class,
// scope and target, read off the path, and for a scale off the body. context
// is the grant's.
func classify(r *http.Request, p apiPath, body []byte, context string) permissions.Action {
	act := permissions.Action{
		Provider: permissions.Kubernetes,
		Scope:    permissions.Scope{Context: context, Namespace: p.namespace},
		Verb:     verbs[r.Method],
		Group:    p.group,
		Kind:     p.resource,
		Name:     p.name,
	}
	if p.kind == resourcePath && act.Group == "" {
		act.Group = "core"
	}
	// A Namespace is not in a namespace, but a rule scoped to one means it too.
	if act.Group == "core" && p.resource == "namespaces" {
		act.Scope.Namespace = p.name
	}
	if p.subresource != "" {
		act.Kind += "/" + p.subresource
	}
	if r.Method == http.MethodDelete && p.name == "" {
		act.Verb = "deletecollection"
	}
	switch {
	case r.Method == http.MethodGet && p.onSecrets():
		act.Class = permissions.SecretRead
	case !isWrite(r, p) || isDryRun(r) && honorsDryRun[act.Group]:
		act.Class = permissions.ReadInside
	case destructive(r, p, act, body):
		act.Class = permissions.Destructive
	default:
		act.Class = permissions.UpstreamWrite
	}
	act.Summary = summary(p, act)
	return act
}

// honorsDryRun are the groups the API server serves itself, which honor
// dryRun. Any other may be an aggregated API, whose server can ignore it and
// apply the write.
var honorsDryRun = map[string]bool{
	"core": true, "apps": true, "batch": true, "autoscaling": true, "policy": true,
	"networking.k8s.io": true, "storage.k8s.io": true, "rbac.authorization.k8s.io": true,
	"apiextensions.k8s.io": true, "apiregistration.k8s.io": true, "certificates.k8s.io": true,
	"admissionregistration.k8s.io": true, "coordination.k8s.io": true, "discovery.k8s.io": true,
	"scheduling.k8s.io": true, "node.k8s.io": true, "events.k8s.io": true,
	"flowcontrol.apiserver.k8s.io": true, "resource.k8s.io": true, "storagemigration.k8s.io": true,
	"internal.apiserver.k8s.io": true, "authentication.k8s.io": true, "authorization.k8s.io": true,
}

// deletedHard are the resources whose delete is class 5, by group then
// resource.
var deletedHard = map[string]map[string]bool{
	"core":                 {"namespaces": true, "nodes": true, "persistentvolumes": true, "persistentvolumeclaims": true},
	"apiextensions.k8s.io": {"customresourcedefinitions": true},
}

// writtenHard are the resources any write of which is class 5, by group then
// resource: who may do what, and what sees or changes every write.
var writtenHard = map[string]map[string]bool{
	"rbac.authorization.k8s.io": {"clusterroles": true, "clusterrolebindings": true, "roles": true, "rolebindings": true},
	"admissionregistration.k8s.io": {
		"validatingwebhookconfigurations": true, "mutatingwebhookconfigurations": true,
		"validatingadmissionpolicies": true, "validatingadmissionpolicybindings": true,
		"mutatingadmissionpolicies": true, "mutatingadmissionpolicybindings": true,
	},
}

// scaled are the resources whose spec.replicas a PUT or PATCH can set to 0, by
// group then resource.
var scaled = map[string]map[string]bool{
	"apps": {"deployments": true, "statefulsets": true, "replicasets": true},
	"core": {"replicationcontrollers": true},
}

// destructive is whether a write that is not a dry run is on the class 5 list.
func destructive(r *http.Request, p apiPath, act permissions.Action, body []byte) bool {
	switch {
	case act.Verb == "deletecollection":
		return true
	case act.Verb == "delete" && p.subresource == "" && deletedHard[act.Group][p.resource]:
		return true
	case writtenHard[act.Group][p.resource]:
		return true
	case act.Group == "certificates.k8s.io" && p.resource == "certificatesigningrequests" && p.subresource == "approval":
		return true
	case p.subresource == "ephemeralcontainers":
		return true
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		return false
	}
	if p.subresource == "scale" || p.subresource == "" && scaled[act.Group][p.resource] {
		return !keepsReplicas(r.Header.Get("Content-Type"), body, p.subresource == "scale")
	}
	return false
}

// keepsReplicas is whether a write that can set replicas is in a form the
// proxy reads whole and leaves at least one; anything else is class 5, so a
// form it does not know asks rather than passes. On a Scale that is a literal
// positive spec.replicas in a JSON, merge or strategic body, or a JSON Patch
// that only adds or replaces /spec/replicas with one. On a workload it is also
// a body that leaves replicas alone, which the API server then keeps or
// defaults to one.
func keepsReplicas(contentType string, body []byte, onScale bool) bool {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "application/json-patch+json":
		return patchKeepsReplicas(body, onScale)
	case "application/apply-patch+yaml":
		if onScale {
			return false
		}
	}
	value, err := decodeBody(mediaType, body)
	if err != nil {
		return false
	}
	return objectKeepsReplicas(value, onScale)
}

// objectKeepsReplicas reads a body that is the object: no $ directive on it or
// its spec, which can replace or drop what it leaves out, and spec.replicas a
// positive count, or on a workload absent.
func objectKeepsReplicas(value any, onScale bool) bool {
	root, ok := value.(map[string]any)
	if !ok || hasDirective(root) {
		return false
	}
	rawSpec, hasSpec := root["spec"]
	if !hasSpec {
		return !onScale
	}
	spec, ok := rawSpec.(map[string]any)
	if !ok || hasDirective(spec) {
		return false
	}
	replicas, hasReplicas := spec["replicas"]
	if !hasReplicas {
		return !onScale
	}
	return positiveCount(replicas)
}

// patchKeepsReplicas reads a JSON Patch, its operations' keys exactly, as the
// API server reads them: an add or replace of /spec/replicas with a positive
// count, and on a workload any operation whose paths lie under /metadata/ or
// under /spec/ beside replicas.
func patchKeepsReplicas(body []byte, onScale bool) bool {
	var ops []map[string]json.RawMessage
	if json.Unmarshal(body, &ops) != nil {
		return false
	}
	for _, op := range ops {
		kind, path := patchString(op["op"]), patchString(op["path"])
		if path == "/spec/replicas" {
			value, err := decodeBody("application/json", op["value"])
			if (kind != "add" && kind != "replace") || err != nil || !positiveCount(value) {
				return false
			}
			continue
		}
		if onScale || !besideReplicas(path) {
			return false
		}
		switch kind {
		case "add", "replace", "remove", "test":
		case "move", "copy":
			if !besideReplicas(patchString(op["from"])) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// besideReplicas is whether a JSON Pointer names something under metadata, or
// under spec other than replicas.
func besideReplicas(path string) bool {
	if strings.HasPrefix(path, "/metadata/") {
		return true
	}
	field, _, _ := strings.Cut(strings.TrimPrefix(path, "/spec/"), "/")
	return strings.HasPrefix(path, "/spec/") && field != "" && field != "replicas"
}

func hasDirective(obj map[string]any) bool {
	for key := range obj {
		if strings.HasPrefix(key, "$") {
			return true
		}
	}
	return false
}

// positiveCount is whether value is a whole number above zero.
func positiveCount(value any) bool {
	n, ok := value.(json.Number)
	if !ok {
		return false
	}
	count, err := n.Int64()
	return err == nil && count > 0
}

// patchString is a JSON Patch member read as a string, "" for one that is
// missing or of another type.
func patchString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// verbWords spell each verb at the head of a summary.
var verbWords = map[string]string{
	"get": "Read", "create": "Create", "update": "Replace", "patch": "Patch",
	"delete": "Delete", "deletecollection": "Delete every",
}

// summary is act in one line, as the note spells it: Delete pod api-7f9c in
// team-a on dev-eks.
func summary(p apiPath, act permissions.Action) string {
	var b strings.Builder
	b.WriteString(verbWords[act.Verb])
	if what := singular(act.Group, p.resource); what != "" {
		b.WriteString(" " + what)
	}
	if p.name != "" {
		b.WriteString(" " + p.name)
	}
	if p.subresource != "" {
		b.WriteString(" (" + p.subresource + ")")
	}
	if p.namespace != "" {
		b.WriteString(" in " + p.namespace)
	}
	if act.Scope.Context != "" {
		b.WriteString(" on " + act.Scope.Context)
	}
	return b.String()
}

// singular is a built-in resource as kubectl prints its kind; any other, a
// custom one included, is its plural as the path gives it, since the proxy
// reads no discovery.
func singular(group, resource string) string {
	if s, ok := singulars[group+"/"+resource]; ok {
		return s
	}
	return resource
}

var singulars = map[string]string{
	"core/pods": "pod", "core/services": "service", "core/configmaps": "configmap", "core/secrets": "secret",
	"core/namespaces": "namespace", "core/nodes": "node", "core/persistentvolumes": "persistentvolume",
	"core/persistentvolumeclaims": "persistentvolumeclaim", "core/serviceaccounts": "serviceaccount",
	"core/endpoints": "endpoints", "core/events": "event", "core/limitranges": "limitrange",
	"core/resourcequotas": "resourcequota", "core/replicationcontrollers": "replicationcontroller",
	"apps/deployments": "deployment", "apps/statefulsets": "statefulset", "apps/daemonsets": "daemonset",
	"apps/replicasets": "replicaset", "apps/controllerrevisions": "controllerrevision",
	"batch/jobs": "job", "batch/cronjobs": "cronjob",
	"autoscaling/horizontalpodautoscalers":                           "horizontalpodautoscaler",
	"policy/poddisruptionbudgets":                                    "poddisruptionbudget",
	"networking.k8s.io/ingresses":                                    "ingress",
	"networking.k8s.io/networkpolicies":                              "networkpolicy",
	"networking.k8s.io/ingressclasses":                               "ingressclass",
	"storage.k8s.io/storageclasses":                                  "storageclass",
	"rbac.authorization.k8s.io/roles":                                "role",
	"rbac.authorization.k8s.io/rolebindings":                         "rolebinding",
	"rbac.authorization.k8s.io/clusterroles":                         "clusterrole",
	"rbac.authorization.k8s.io/clusterrolebindings":                  "clusterrolebinding",
	"apiextensions.k8s.io/customresourcedefinitions":                 "customresourcedefinition",
	"certificates.k8s.io/certificatesigningrequests":                 "certificatesigningrequest",
	"admissionregistration.k8s.io/validatingwebhookconfigurations":   "validatingwebhookconfiguration",
	"admissionregistration.k8s.io/mutatingwebhookconfigurations":     "mutatingwebhookconfiguration",
	"admissionregistration.k8s.io/validatingadmissionpolicies":       "validatingadmissionpolicy",
	"admissionregistration.k8s.io/validatingadmissionpolicybindings": "validatingadmissionpolicybinding",
	"coordination.k8s.io/leases":                                     "lease",
	"discovery.k8s.io/endpointslices":                                "endpointslice",
	"scheduling.k8s.io/priorityclasses":                              "priorityclass",
}
