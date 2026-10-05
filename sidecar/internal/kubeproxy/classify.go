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
	"cmp"
	"encoding/json"
	"mime"
	"net/http"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
)

// Destructive is the class 5 list in words, as Settings shows it under
// Always asks: one line per item destructive decides on.
var Destructive = []string{
	"Deleting a namespace, node, persistent volume, persistent volume claim or custom resource definition, or finalizing a namespace",
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

// classify is what a request the policy passed does, for a decision: its
// class, scope and target, read off the path, and for a replica write or a
// Namespace's create off the body, which checkBody has passed. context is the
// grant's.
func classify(r *http.Request, p apiPath, body []byte, context string) permissions.Action {
	act := permissions.Action{
		Context: context, Namespace: p.namespace,
		Verb: verbs[r.Method], Group: p.group, Kind: p.resource, Name: p.name,
		DryRun: isDryRun(r),
	}
	if p.kind == resourcePath && act.Group == "" {
		act.Group = "core"
	}
	// A Namespace is not in a namespace, but a rule scoped to one means it too.
	// A create names it in the body.
	if act.Group == "core" && p.resource == "namespaces" {
		if act.Name == "" && r.Method == http.MethodPost {
			act.Name = createdName(r.Header.Get("Content-Type"), body)
		}
		act.Namespace = act.Name
	}
	if p.subresource != "" {
		act.Kind += "/" + p.subresource
	}
	if r.Method == http.MethodDelete && p.name == "" {
		act.Verb = "deletecollection"
	}
	// A read's verb is the API server's, which a Secret read's rule can name.
	if r.Method == http.MethodGet && p.kind == resourcePath {
		switch {
		case isWatch(p, r.URL.Query()):
			act.Verb = "watch"
		case p.name == "":
			act.Verb = "list"
		}
	}
	switch {
	case r.Method == http.MethodGet && p.onSecrets():
		act.Class = permissions.SecretRead
	case !isWrite(r, p) || act.DryRun && honorsDryRun[act.Group+"/"+p.version]:
		act.Class = permissions.ReadInside
	case destructive(r, p, act, body):
		act.Class = permissions.Destructive
	default:
		act.Class = permissions.UpstreamWrite
	}
	act.Summary = summary(p, act)
	return act
}

// honorsDryRun are the group versions the API server serves itself, which
// honor dryRun. The aggregator routes by group and version, so any other —
// even a new version of a built-in group — may be an aggregated API, whose
// server can ignore dryRun and apply the write. Only stable versions are
// listed: a missing one asks, which is safe.
var honorsDryRun = map[string]bool{
	"core/v1": true, "apps/v1": true, "batch/v1": true, "autoscaling/v1": true, "autoscaling/v2": true,
	"policy/v1": true, "networking.k8s.io/v1": true, "storage.k8s.io/v1": true,
	"rbac.authorization.k8s.io/v1": true, "apiextensions.k8s.io/v1": true, "apiregistration.k8s.io/v1": true,
	"certificates.k8s.io/v1": true, "admissionregistration.k8s.io/v1": true, "coordination.k8s.io/v1": true,
	"discovery.k8s.io/v1": true, "scheduling.k8s.io/v1": true, "node.k8s.io/v1": true,
	"events.k8s.io/v1": true, "flowcontrol.apiserver.k8s.io/v1": true, "resource.k8s.io/v1": true,
	"authentication.k8s.io/v1": true, "authorization.k8s.io/v1": true,
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
	// Finalize clears what holds a terminating namespace, so it completes the
	// delete above.
	case act.Group == "core" && p.resource == "namespaces" && p.subresource == "finalize":
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

// createdName is the metadata.name a create's body gives the object, "" for a
// body that gives none.
func createdName(contentType string, body []byte) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	value, err := decodeBody(mediaType, body)
	if err != nil {
		return ""
	}
	root, _ := value.(map[string]any)
	metadata, _ := root["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	return name
}

// keepsReplicas is whether a write that can set replicas is in a form the
// proxy reads whole and that leaves at least one. Anything else is class 5, so
// a form the proxy does not know asks rather than passes. On a Scale that is a
// literal positive spec.replicas in a JSON, merge or strategic body, or a JSON
// Patch that only adds or replaces /spec/replicas with one. On a workload it is
// also a body that leaves replicas alone, which the API server then keeps or
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
// count, and on a workload an add, replace, remove or test under /metadata/ or
// under /spec/ beside replicas. A move or copy is never plain: its value is
// not in the body.
func patchKeepsReplicas(body []byte, onScale bool) bool {
	var ops []map[string]json.RawMessage
	if json.Unmarshal(body, &ops) != nil {
		return false
	}
	for _, op := range ops {
		kind, path := patchString(op["op"]), patchString(op["path"])
		switch {
		case path == "/spec/replicas":
			value, err := decodeBody("application/json", op["value"])
			if (kind != "add" && kind != "replace") || err != nil || !positiveCount(value) {
				return false
			}
		case onScale || !besideReplicas(path):
			return false
		case kind != "add" && kind != "replace" && kind != "remove" && kind != "test":
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
	"delete": "Delete", "deletecollection": "Delete all",
}

// summary is act in one line, naming the target as kubectl does: "Delete
// pods/api-7f9c in team-a on dev-eks". A Secret read names what it shows:
// "Show Secret db-creds in team-a on dev-eks", "Watch Secret data on dev-eks".
func summary(p apiPath, act permissions.Action) string {
	var line string
	switch {
	case act.Class == permissions.SecretRead && act.Verb == "watch":
		line = "Watch Secret " + cmp.Or(act.Name, "data")
	case act.Class == permissions.SecretRead:
		line = "Show Secret " + cmp.Or(act.Name, "data")
	default:
		line = verbWords[act.Verb] + " " + p.resource
		if act.Name != "" {
			line += "/" + act.Name
		}
		if p.subresource != "" {
			line += "/" + p.subresource
		}
	}
	if p.namespace != "" {
		line += " in " + p.namespace
	}
	if act.Context != "" {
		line += " on " + act.Context
	}
	return line
}
