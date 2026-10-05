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

import "strings"

// Class is how much an action can do, numbered as the agent-security note
// numbers them. 1 and 2 are also the classes of a folder grant, and 3 is
// unused: the network is a switch, not a list of hosts.
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

// ClusterScope is the Namespace of a rule over cluster-scoped objects alone.
// No namespace name holds a bracket and no pattern character is one, so it is
// a word of its own in a field a pattern otherwise fills.
const ClusterScope = "[cluster]"

// Rule is one line of policy. Context, Namespace, Verb and Kind are patterns
// (Match), and an unset one matches anything. Group is exact: "" is unset,
// "core" the core group. Kind is the resource, or "deployments/scale" for a
// subresource. Namespace is ClusterScope for
// cluster-scoped objects. A rule naming a Folder is a folder grant: it matches
// no action, and only the builder of a session's folders reads it.
type Rule struct {
	ID        string `json:"id"`
	Effect    Effect `json:"effect"`
	Class     Class  `json:"class"`
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Verb      string `json:"verb,omitempty"`
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind,omitempty"`
	// Inside narrows Namespace to what is in the namespace, leaving out the
	// Namespace object, which carries its own name as its namespace.
	Inside bool `json:"inside,omitempty"`
	// Command is a rule one command's answer added, kept in memory for that
	// command alone and never stored.
	Command bool   `json:"-"`
	Folder  string `json:"folder,omitempty"` // classes 1 and 2: the folder, absolute and resolved
}

// Action is one classified request, as the proxy read it. It is stored with
// the approval that records it.
type Action struct {
	Class     Class  `json:"class"`
	Context   string `json:"context"`   // the kube-context the grant was made for; "" for a record naming none
	Namespace string `json:"namespace"` // "" for a cluster-scoped request
	Verb      string `json:"verb"`      // get, create, update, patch, delete, deletecollection
	Group     string `json:"group"`     // the API group, "core" for the core group
	Kind      string `json:"kind"`      // the resource, "deployments/scale" for a subresource
	Name      string `json:"name"`      // the object's name, when it has one
	Summary   string `json:"summary"`   // one line for a request or a refusal, written by the classifier
	// DryRun is a request for a dry run. No rule names one, so a rule written
	// from it would allow the real write.
	DryRun bool `json:"dryRun"`
}

// Decision is what Decide answers.
type Decision string

const (
	Allowed  Decision = "allowed"
	Prompted Decision = "prompted"
	Denied   Decision = "denied"
)

// Duration is how long the user's approval of an action holds: this request,
// the rest of the command that sent it, the chat, or always.
type Duration string

const (
	DurationOnce    Duration = "once"
	DurationCommand Duration = "command"
	DurationChat    Duration = "chat"
	DurationAlways  Duration = "always"
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

// Grantable is whether an answer to act may write a rule that allows it. Only
// an Unmatched verdict is lifted by an Allow. A dry run is not, since no rule
// names one and a rule written from it would allow the real write; nor is an
// action with no context, since an unset rule field matches every context.
func Grantable(v Verdict, act Action) bool {
	return v == Unmatched && !act.DryRun && act.Context != ""
}

// GrantRule is the rule a chat or always answer to act writes: an Allow of
// its class in its context and namespace, each a literal. A cluster-scoped
// action, and a Namespace object, also name the group and resource: an unset
// namespace matches every namespace, and a Namespace carries its own name as
// its namespace, so naming that alone would allow every write inside it. Any
// other action's rule is Inside, so allowing what is in a namespace never
// allows changing the Namespace itself. The writer sets the ID.
func GrantRule(act Action) Rule {
	r := Rule{Effect: Allow, Class: act.Class, Context: Literal(act.Context), Namespace: Literal(act.Namespace)}
	if act.Namespace == "" || isNamespace(act) {
		r.Group, r.Kind = act.Group, Literal(act.Kind)
	} else {
		r.Inside = true
	}
	return r
}

// isNamespace is whether act writes a Namespace object or its subresource.
func isNamespace(act Action) bool {
	resource, _, _ := strings.Cut(act.Kind, "/")
	return act.Group == "core" && resource == "namespaces"
}

// CommandRule is the rule a command answer to act adds for the rest of the
// command: GrantRule's, naming the verb and the resource as well, since the
// user saw one change and allows its repeats. Naming the resource leaves the
// Namespace out unless act wrote one, so it is never Inside.
func CommandRule(act Action) Rule {
	r := GrantRule(act)
	r.Verb, r.Group, r.Kind, r.Command, r.Inside = Literal(act.Verb), act.Group, Literal(act.Kind), true, false
	return r
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
// cluster write. An Inside rule never matches a Namespace object, and a folder
// grant matches nothing, so it never reaches a verdict.
func (r Rule) Matches(act Action) bool {
	return r.Folder == "" &&
		(r.Class == act.Class || r.Class == UpstreamWrite && act.Class == Destructive) &&
		matchSet(r.Context, act.Context) &&
		matchNamespace(r.Namespace, act.Namespace) &&
		!(r.Inside && isNamespace(act)) &&
		matchSet(r.Verb, act.Verb) &&
		(r.Group == "" || r.Group == act.Group) &&
		matchKind(r.Kind, act.Kind)
}

// matchSet is whether a rule field matches: unset matches anything.
func matchSet(pattern, value string) bool {
	return pattern == "" || Match(pattern, value)
}

// matchNamespace is whether a rule's Namespace matches: unset matches
// anything, ClusterScope an action in no namespace, and a pattern an action in
// one, so a rule for "*" covers every namespace and nothing cluster-scoped.
func matchNamespace(pattern, namespace string) bool {
	switch {
	case pattern == "":
		return true
	case pattern == ClusterScope:
		return namespace == ""
	}
	return namespace != "" && Match(pattern, namespace)
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

// folderNouns is what a folder grant of each class lets a command do there.
var folderNouns = map[Class]string{ReadInside: "reads", WriteInside: "reads and writes"}

// quoted is s bare when it holds no space, " or \, else in quotes with " and \
// escaped, so a line names one value however it is spelled.
func quoted(s string) string {
	if !strings.ContainsAny(s, ` "\`) {
		return s
	}
	return quote(s)
}

// Line is the rule in the user's words: "Allow cluster writes in dev-eks /
// team-a", "Deny destructive delete of core namespaces everywhere", "Deny
// patch of core nodes cluster-wide in prod", or "Allow reads of /Users/me/code".
// An Inside rule reads "inside" for "in". Each pattern field is drawn by field,
// so a line reads one way whatever a value holds.
func (r Rule) Line() string {
	if r.Folder != "" {
		return effectWords[r.Effect] + " " + folderNouns[r.Class] + " of " + quoted(r.Folder)
	}
	if r.Command {
		return r.scopeLine() + " for this command"
	}
	return r.scopeLine()
}

func (r Rule) scopeLine() string {
	what := classNouns[r.Class]
	if r.Verb != "" || r.Group != "" || r.Kind != "" {
		parts := []string{"writes", "of"}
		if r.Verb != "" {
			parts[0] = fieldWords(r.Verb)
		}
		if r.Class == Destructive {
			parts = append([]string{"destructive"}, parts...)
		}
		if r.Group != "" {
			parts = append(parts, r.Group)
		}
		switch {
		case r.Kind != "":
			parts = append(parts, fieldWords(r.Kind))
		case r.Group != "":
			parts = append(parts, "resources")
		default:
			parts = append(parts, "anything")
		}
		what = strings.Join(parts, " ")
	}
	line := effectWords[r.Effect] + " " + what
	switch {
	case r.Namespace == ClusterScope:
		line += " cluster-wide"
		if r.Context != "" {
			line += " in " + fieldWords(r.Context)
		}
		return line
	case r.Context == "" && r.Namespace == "":
		return line + " everywhere"
	case r.Namespace == "":
		return line + " in " + fieldWords(r.Context)
	}
	context := "any context"
	if r.Context != "" {
		context = fieldWords(r.Context)
	}
	in := " in "
	if r.Inside {
		in = " inside "
	}
	return line + in + context + " / " + fieldWords(r.Namespace)
}

// fieldWords draws one pattern field of a line. Name characters alone are
// drawn bare, a * or ? in them a glob. A literal holding a glob character, a
// space, a " or a \ is drawn unescaped in quotes. Anything else is drawn as
// written, in quotes, after "matching". So " / " outside quotes always
// separates the context from the namespace.
func fieldWords(v string) string {
	if !strings.ContainsAny(v, ` "\`) {
		return v
	}
	if plain, ok := unescapeLiteral(v); ok {
		return quote(plain)
	}
	return "matching " + quote(v)
}

// unescapeLiteral is the value pattern matches alone, false for a pattern
// Literal would not have written.
func unescapeLiteral(pattern string) (string, bool) {
	var b strings.Builder
	escaped := false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
			continue
		case r == '*' || r == '?':
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), !escaped && Literal(b.String()) == pattern
}

// quote is s in double quotes, each " and \ inside preceded by \.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
