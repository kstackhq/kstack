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

// Package permissions decides, for one classified cluster write, whether it
// runs, asks or is refused: by its class, the approval mode, and the rules.
// Authorization is binary and fails closed: the policy permits an action or
// denies it, a forbid wins over a permit, and nothing matching is a denial. A
// prompt is a denial the user may lift. A leaf: the proxy that enforces it
// imports it, and it imports nothing of ours.
package permissions

import (
	"cmp"
	"strings"
)

// Class is how much an action can do, numbered as the agent-security note
// numbers them. Only 1, 4, 5 and 6 are classified today; 2 and 3 arrive with
// the folder and host steps.
type Class int

const (
	ReadInside    Class = 1 // read inside the sandbox
	WriteInside   Class = 2 // write the workspace or a granted folder
	NewHost       Class = 3 // reach a host not on the allowlist
	UpstreamWrite Class = 4 // change the cluster
	Destructive   Class = 5 // a curated list of high blast-radius writes
	SecretRead    Class = 6 // read Kubernetes Secret data
)

// Mode is which classes ask. ReadOnly refuses writes; Ask asks for every
// write; Auto asks for class 5 alone.
type Mode string

const (
	ReadOnly Mode = "read-only"
	Ask      Mode = "ask"
	Auto     Mode = "auto"
)

// Effect is what a rule does to an action it matches. Deny wins over Ask, and
// Ask over Allow.
type Effect string

const (
	Allow  Effect = "allow"
	Deny   Effect = "deny"
	AskFor Effect = "ask" // always prompt, unless the mode refuses first
)

// Rule is one line of policy. Every field but ID, Effect, Class and Group is a
// pattern (Match), and an unset one matches anything. Group is exact: "" is
// unset, "core" the core group. Kind is the resource, or "deployments/scale"
// for a subresource.
type Rule struct {
	ID        string `json:"id"`
	Effect    Effect `json:"effect"`
	Class     Class  `json:"class"`
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Verb      string `json:"verb,omitempty"`
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// Action is one classified request, as the proxy read it.
type Action struct {
	Class     Class
	Context   string // the kube-context the grant was made for; "" for a record naming none
	Namespace string // "" for a cluster-scoped request
	Verb      string // get, create, update, patch, delete, deletecollection
	Group     string // the API group, "core" for the core group
	Kind      string // the resource, "deployments/scale" for a subresource
	Name      string // the object's name, when it has one
	Summary   string // one line for a refusal, written by the classifier
}

// Decision is what Decide answers.
type Decision string

const (
	Allowed  Decision = "allowed"
	Prompted Decision = "prompted"
	Denied   Decision = "denied"
)

// Verdict is what the policy says of an action before anyone is asked:
// permitted, or denied in one of three ways. The strongest of what matched
// wins, so a forbid beats a permit, and the verdicts are numbered in that
// order: the greatest that applies is the answer, and the zero value is the
// default denial.
type Verdict int

const (
	// Unmatched: nothing matched. The default denial, which an answer lifts
	// once and a grant lifts for good.
	Unmatched Verdict = iota
	// Permit: a permit matched and no forbid did. The action runs.
	Permit
	// Forbid: a forbid an answer lifts once and no grant lifts, since a
	// forbid wins over any permit: an AskFor rule, or class 5.
	Forbid
	// Refuse: a forbid no answer lifts: a Deny rule, or the mode.
	Refuse
)

// Outcome is what the proxies do with a verdict: a permit runs, a refusal is
// refused, and either other denial is put to the user.
func (v Verdict) Outcome() Decision {
	switch v {
	case Permit:
		return Allowed
	case Refuse:
		return Denied
	}
	return Prompted
}

// Policy is what a session brings to a decision: the context's mode and the
// rules that apply. Their order picks the reason alone, never the verdict.
type Policy struct {
	Mode  Mode
	Rules []Rule
}

// Decide answers what happens to act under p, and why, in the user's words:
// the policy's verdict, then what the proxies do with it.
func (p Policy) Decide(act Action) (Decision, string) {
	v, why := p.Authorize(act)
	return v.Outcome(), why
}

// Authorize is the policy's verdict on act, and the reason in the user's
// words: the strongest of what matched, a rule's line when a rule decided it.
// The checks run strongest first, so the first that applies is the verdict,
// and which rules match decides it whatever their order.
func (p Policy) Authorize(act Action) (Verdict, string) {
	if r, ok := p.first(Deny, act); ok {
		return Refuse, "a rule denies it: " + r.Line()
	}
	if p.Mode == ReadOnly && act.Class >= NewHost && act.Class <= Destructive {
		return Refuse, "this context is read-only"
	}
	if r, ok := p.first(AskFor, act); ok {
		return Forbid, "a rule asks for it: " + r.Line()
	}
	if act.Class == Destructive {
		return Forbid, "it always asks"
	}
	if act.Class == ReadInside || act.Class == WriteInside {
		return Permit, "it changes nothing in the cluster"
	}
	if r, ok := p.first(Allow, act); ok {
		return Permit, "a rule allows it: " + r.Line()
	}
	if p.Mode == Auto {
		return Permit, "auto mode"
	}
	return Unmatched, string(p.Mode) + " mode"
}

// first is the first rule of effect that matches act.
func (p Policy) first(effect Effect, act Action) (Rule, bool) {
	for _, r := range p.Rules {
		if r.Effect == effect && r.Matches(act) {
			return r, true
		}
	}
	return Rule{}, false
}

// Refused stands in for a rule Kstack could not read, which may have been a
// Deny: every cluster write refused.
var Refused = Rule{ID: "refused", Effect: Deny, Class: UpstreamWrite}

// Matches is whether r applies to act: a class that covers act's, and every
// set field matching. Class 4 covers class 5, since a destructive write is a
// cluster write. A set Namespace never matches an action in none, so a rule
// for "*" covers every namespace and nothing cluster-scoped.
func (r Rule) Matches(act Action) bool {
	return (r.Class == act.Class || r.Class == UpstreamWrite && act.Class == Destructive) &&
		matchSet(r.Context, act.Context) &&
		(r.Namespace == "" || act.Namespace != "" && Match(r.Namespace, act.Namespace)) &&
		matchSet(r.Verb, act.Verb) &&
		(r.Group == "" || r.Group == act.Group) &&
		matchKind(r.Kind, act.Kind)
}

// matchSet is whether a rule field matches: unset matches anything.
func matchSet(pattern, value string) bool {
	return pattern == "" || Match(pattern, value)
}

// matchKind is whether a rule's Kind matches. A bare * matches every kind,
// subresources included. Any other Kind with no / matches the resource alone,
// since * crosses /, and covers its scale, which writes the object's own
// replicas, so "deployments" covers "deployments/scale". Any other subresource
// does something else under the same verb (a create of pods/eviction removes a
// pod), so a rule names it to cover it.
func matchKind(pattern, kind string) bool {
	if pattern == "" || pattern == "*" || strings.Contains(pattern, "/") {
		return matchSet(pattern, kind)
	}
	resource, sub, _ := strings.Cut(kind, "/")
	return (sub == "" || sub == "scale") && Match(pattern, resource)
}

var (
	effectWords = map[Effect]string{Allow: "Allow", Deny: "Deny", AskFor: "Ask for"}
	classNouns  = map[Class]string{
		ReadInside:    "reads inside the sandbox",
		WriteInside:   "writes inside the sandbox",
		NewHost:       "new hosts",
		UpstreamWrite: "cluster writes",
		Destructive:   "destructive cluster writes",
		SecretRead:    "Secret reads",
	}
)

// Line is the rule in the user's words: "Allow cluster writes in dev-eks /
// team-a", or "Deny destructive delete of core namespaces everywhere".
func (r Rule) Line() string {
	what := classNouns[r.Class]
	if r.Verb != "" || r.Group != "" || r.Kind != "" {
		parts := []string{cmp.Or(r.Verb, "writes"), "of"}
		if r.Class == Destructive {
			parts = append([]string{"destructive"}, parts...)
		}
		if r.Group != "" {
			parts = append(parts, r.Group)
		}
		switch {
		case r.Kind != "":
			parts = append(parts, r.Kind)
		case r.Group != "":
			parts = append(parts, "resources")
		default:
			parts = append(parts, "anything")
		}
		what = strings.Join(parts, " ")
	}
	switch {
	case r.Context == "" && r.Namespace == "":
		return effectWords[r.Effect] + " " + what + " everywhere"
	case r.Namespace == "":
		return effectWords[r.Effect] + " " + what + " in " + r.Context
	}
	return effectWords[r.Effect] + " " + what + " in " + cmp.Or(r.Context, "any context") + " / " + r.Namespace
}
