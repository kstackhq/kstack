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

// Package permissions decides, for one classified action, whether it runs,
// asks or is refused: by its class, the approval mode, and the rules. A leaf:
// the proxies that enforce it import it, and it imports nothing of ours.
package permissions

import (
	"cmp"
	"strings"
)

// Class is how much an action can do, as the note numbers them.
type Class int

const (
	ReadInside    Class = 1 // read inside the sandbox
	WriteInside   Class = 2 // write the workspace or a granted folder
	NewHost       Class = 3 // reach a host not on the allowlist
	UpstreamWrite Class = 4 // change the cluster
	Destructive   Class = 5 // a curated list of high blast-radius writes
	SecretRead    Class = 6 // read Kubernetes Secret data
)

// Mode is which classes ask. ReadOnly refuses writes and new hosts; Ask asks
// for everything past class 2; Auto asks for class 5 alone.
type Mode string

const (
	ReadOnly Mode = "read-only"
	Ask      Mode = "ask"
	Auto     Mode = "auto"
)

// Provider is whose action it is, and decides which Scope fields apply.
type Provider string

const (
	Kubernetes Provider = "k8s"
	Net        Provider = "net"  // step 4C
	Path       Provider = "path" // step 4D
)

// Scope is where an action lands. On a rule each field is a pattern (Match;
// "" matches everything): context and namespace for Kubernetes, host for net,
// folder for path.
type Scope struct {
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Host      string `json:"host,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

// Effect is what a rule does to an action it matches. Deny wins over Ask, and
// Ask over Allow.
type Effect string

const (
	Allow  Effect = "allow"
	Deny   Effect = "deny"
	AskFor Effect = "ask" // always prompt, unless the mode refuses first
)

// Rule is one line of policy. Verb, Group and Kind narrow a Kubernetes rule
// ("delete" of core "namespaces"); unset matches all. Group is a literal: ""
// is unset, "core" the core group.
type Rule struct {
	ID       string   `json:"id"`
	Effect   Effect   `json:"effect"`
	Class    Class    `json:"class"`
	Provider Provider `json:"provider"`
	Scope    Scope    `json:"scope,omitzero"`
	Verb     string   `json:"verb,omitempty"`
	Group    string   `json:"group,omitempty"`
	Kind     string   `json:"kind,omitempty"`
}

// Action is one classified request: what a prompt says and what Decide reads.
type Action struct {
	Provider Provider `json:"provider"`
	Class    Class    `json:"class"`
	Scope    Scope    `json:"scope,omitzero"`
	Verb     string   `json:"verb"`              // get, create, update, patch, delete, deletecollection; CONNECT for a host
	Group    string   `json:"group,omitempty"`   // the Kubernetes API group, "core" for the core group
	Kind     string   `json:"kind,omitempty"`    // the resource, "deployments/scale" for a subresource; a port for a host
	Name     string   `json:"name,omitempty"`    // the object's name, when it has one
	Summary  string   `json:"summary,omitempty"` // one line for the prompt, written by the classifier
}

// Enforced is whether a rule of class for provider acts on anything yet: only
// Kubernetes cluster writes reach Decide. The step that wires a proxy to
// another class adds it here.
func Enforced(provider Provider, class Class) bool {
	return provider == Kubernetes && (class == UpstreamWrite || class == Destructive)
}

// Decision is what Decide answers.
type Decision string

const (
	Allowed  Decision = "allowed"
	Prompted Decision = "prompted"
	Denied   Decision = "denied"
)

// Policy is what a session brings to Decide: its mode, and whether anyone can
// be asked. With NoPrompts every Prompted becomes Denied.
type Policy struct {
	Mode      Mode
	NoPrompts bool
}

// Reason is what decided: one of a rule, the mode, or the class, or that
// nobody could be asked.
type Reason struct {
	Rule      *Rule
	Mode      Mode
	Class     Class
	NoPrompts bool
}

// String is the reason in the user's words, as a refusal and a record name it.
func (r Reason) String() string {
	switch {
	case r.NoPrompts:
		return "nobody can be asked"
	case r.Rule != nil:
		return "a rule " + effectVerbs[r.Rule.Effect] + " it: " + r.Rule.String()
	case r.Mode == ReadOnly:
		return "this context is read-only"
	case r.Mode != "":
		return string(r.Mode) + " mode"
	case r.Class == Destructive:
		return "it always asks"
	case r.Class == ReadInside:
		return "it changes nothing"
	case r.Class == WriteInside:
		return "it stays inside the sandbox"
	}
	return ""
}

var effectVerbs = map[Effect]string{Allow: "allows", Deny: "denies", AskFor: "asks for"}

// Decide answers what happens to act under p and rules: the first of these
// that applies. The mode's refusal comes before every AskFor rule and class
// 5's prompt, so neither turns a read-only context's refusal into a prompt.
func Decide(p Policy, rules []Rule, act Action) (Decision, Reason) {
	d, why := decide(p.Mode, rules, act)
	if d == Prompted && p.NoPrompts {
		return Denied, Reason{NoPrompts: true}
	}
	return d, why
}

func decide(mode Mode, rules []Rule, act Action) (Decision, Reason) {
	if act.Class == ReadInside || act.Class == WriteInside {
		return Allowed, Reason{Class: act.Class}
	}
	if r := firstMatch(rules, Deny, act); r != nil {
		return Denied, Reason{Rule: r}
	}
	if mode == ReadOnly && act.Class != SecretRead {
		return Denied, Reason{Mode: ReadOnly}
	}
	if r := firstMatch(rules, AskFor, act); r != nil {
		return Prompted, Reason{Rule: r}
	}
	if act.Class == Destructive {
		return Prompted, Reason{Class: Destructive}
	}
	if r := firstMatch(rules, Allow, act); r != nil {
		return Allowed, Reason{Rule: r}
	}
	if mode == Auto {
		return Allowed, Reason{Mode: Auto}
	}
	return Prompted, Reason{Mode: mode}
}

// Refused stands in for a rule Kstack could not read, which may have been a
// Deny: every cluster write refused. A later provider adds its own beside it.
var Refused = Rule{ID: "refused", Effect: Deny, Class: UpstreamWrite, Provider: Kubernetes}

func firstMatch(rules []Rule, effect Effect, act Action) *Rule {
	for i := range rules {
		if rules[i].Effect == effect && rules[i].Matches(act) {
			return &rules[i]
		}
	}
	return nil
}

// Matches is whether r applies to act: the same provider, a class that covers
// act's, and every set field matching. Class 4 covers class 5, since a
// destructive write is a cluster write.
func (r Rule) Matches(act Action) bool {
	return r.Provider == act.Provider &&
		(r.Class == act.Class || r.Class == UpstreamWrite && act.Class == Destructive) &&
		matchSet(r.Scope.Context, act.Scope.Context) &&
		matchSet(r.Scope.Namespace, act.Scope.Namespace) &&
		matchSet(r.Scope.Host, act.Scope.Host) &&
		matchSet(r.Scope.Folder, act.Scope.Folder) &&
		matchSet(r.Verb, act.Verb) &&
		(r.Group == "" || r.Group == act.Group) &&
		matchKind(r.Kind, act.Kind)
}

// matchSet is whether a rule field matches: unset matches anything.
func matchSet(pattern, value string) bool {
	return pattern == "" || Match(pattern, value)
}

// matchKind is whether a rule's Kind matches: one with no / also matches the
// resource's scale, which writes the object's own replicas, so patch of
// deployments covers deployments/scale. Any other subresource does something
// else under the same verb (a create of pods/eviction removes a pod), so a
// rule names it to cover it.
func matchKind(pattern, kind string) bool {
	if matchSet(pattern, kind) {
		return true
	}
	resource, sub, _ := strings.Cut(kind, "/")
	return sub == "scale" && !strings.Contains(pattern, "/") && Match(pattern, resource)
}

// classNouns name each class in a rule's line.
var classNouns = map[Class]string{
	ReadInside:    "reads inside the sandbox",
	WriteInside:   "writes inside the sandbox",
	NewHost:       "new hosts",
	UpstreamWrite: "cluster writes",
	Destructive:   "destructive cluster writes",
	SecretRead:    "Secret reads",
}

var effectWords = map[Effect]string{Allow: "Allow", Deny: "Deny", AskFor: "Ask for"}

// String is the rule as one line in the user's terms: Allow cluster writes in
// dev-eks / team-a.
func (r Rule) String() string {
	what := classNouns[r.Class]
	if r.Verb != "" || r.Group != "" || r.Kind != "" {
		parts := []string{cmp.Or(r.Verb, "writes"), "of"}
		if r.Group != "" {
			parts = append(parts, r.Group)
		}
		kind := cmp.Or(r.Kind, "anything")
		if r.Kind == "" && r.Group != "" {
			kind = "resources"
		}
		what = strings.Join(append(parts, kind), " ")
	}
	return effectWords[r.Effect] + " " + what + r.Scope.where()
}

// where is a scope as a rule's line ends: " in dev-eks / team-a", or
// " everywhere".
func (s Scope) where() string {
	switch {
	case s.Host != "":
		return " to " + s.Host
	case s.Folder != "":
		return " in " + s.Folder
	case s.Context == "" && s.Namespace == "":
		return " everywhere"
	case s.Namespace == "":
		return " in " + s.Context
	}
	return " in " + cmp.Or(s.Context, "any context") + " / " + s.Namespace
}
