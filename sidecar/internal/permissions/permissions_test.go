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

package permissions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patch is a patch of the deployment api in dev / team-a, of class c.
func patch(c Class) Action {
	return Action{Class: c, Context: "dev", Namespace: "team-a", Verb: "patch", Group: "apps", Kind: "deployments", Name: "api"}
}

func TestDecideFollowsTheModeTable(t *testing.T) {
	trusted := []Rule{{ID: "t", Effect: Allow, Class: UpstreamWrite, Context: "dev", Namespace: "team-a"}}
	for name, c := range map[string]struct {
		policy Policy
		want   map[Class]Decision
	}{
		"read-only": {Policy{Mode: ReadOnly}, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Denied, UpstreamWrite: Denied, Destructive: Denied, SecretRead: Prompted,
		}},
		"ask": {Policy{Mode: Ask}, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Prompted, UpstreamWrite: Prompted, Destructive: Prompted, SecretRead: Prompted,
		}},
		"trusted": {Policy{Mode: Ask, Rules: trusted}, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Prompted, UpstreamWrite: Allowed, Destructive: Prompted, SecretRead: Prompted,
		}},
		"auto": {Policy{Mode: Auto}, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Allowed, UpstreamWrite: Allowed, Destructive: Prompted, SecretRead: Allowed,
		}},
	} {
		for class, want := range c.want {
			got, _ := c.policy.Decide(patch(class))
			assert.Equal(t, want, got, "%s, class %d", name, class)
		}
	}
}

func TestClassFiveAsksInEveryMode(t *testing.T) {
	allow := []Rule{{ID: "a", Effect: Allow, Class: UpstreamWrite}}
	for _, mode := range []Mode{Ask, Auto} {
		for _, rules := range [][]Rule{nil, allow} {
			got, why := Policy{Mode: mode, Rules: rules}.Decide(patch(Destructive))
			assert.Equal(t, Prompted, got, "%s", mode)
			assert.Equal(t, "it always asks", why)
		}
	}
	got, why := Policy{Mode: ReadOnly, Rules: allow}.Decide(patch(Destructive))
	assert.Equal(t, Denied, got)
	assert.Equal(t, "this context is read-only", why)
}

func TestDenyWinsOverAskWinsOverAllow(t *testing.T) {
	rule := func(e Effect) Rule { return Rule{ID: string(e), Effect: e, Class: UpstreamWrite} }
	for _, c := range []struct {
		rules []Rule
		want  Decision
		why   string
	}{
		{[]Rule{rule(Allow), rule(AskFor), rule(Deny)}, Denied, "a rule denies it: Deny cluster writes everywhere"},
		{[]Rule{rule(Allow), rule(AskFor)}, Prompted, "a rule asks for it: Ask for cluster writes everywhere"},
		{[]Rule{rule(Allow)}, Allowed, "a rule allows it: Allow cluster writes everywhere"},
	} {
		got, why := Policy{Mode: Auto, Rules: c.rules}.Decide(patch(UpstreamWrite))
		assert.Equal(t, c.want, got)
		assert.Equal(t, c.why, why)
	}
}

func TestAClassFourRuleCoversClassFive(t *testing.T) {
	for _, c := range []struct {
		effect Effect
		want   Decision
	}{{Deny, Denied}, {AskFor, Prompted}, {Allow, Prompted}} {
		rules := []Rule{{ID: "r", Effect: c.effect, Class: UpstreamWrite, Context: "dev", Namespace: "team-a"}}
		got, _ := Policy{Mode: Auto, Rules: rules}.Decide(patch(Destructive))
		assert.Equal(t, c.want, got, "%s", c.effect)
	}
	rules := []Rule{{ID: "r", Effect: Deny, Class: Destructive}}
	got, _ := Policy{Mode: Auto, Rules: rules}.Decide(patch(UpstreamWrite))
	assert.Equal(t, Allowed, got, "a class 5 rule leaves class 4 alone")
}

func TestARuleMatchesByGroupAndSubresource(t *testing.T) {
	allow := func(group, kind string) Policy {
		return Policy{Mode: Ask, Rules: []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Group: group, Kind: kind}}}
	}
	pods := Action{Class: UpstreamWrite, Verb: "delete", Group: "example.com", Kind: "pods"}
	got, _ := allow("core", "pods").Decide(pods)
	assert.Equal(t, Prompted, got, "core pods is not another group's pods")
	pods.Group = "core"
	got, _ = allow("core", "pods").Decide(pods)
	assert.Equal(t, Allowed, got)

	scale := Action{Class: UpstreamWrite, Verb: "patch", Group: "apps", Kind: "deployments/scale"}
	got, _ = allow("apps", "deployments").Decide(scale)
	assert.Equal(t, Allowed, got, "a resource covers its scale")
	status := Action{Class: UpstreamWrite, Verb: "update", Group: "apps", Kind: "deployments/status"}
	got, _ = allow("apps", "deployments").Decide(status)
	assert.Equal(t, Prompted, got, "a resource does not cover its other subresources")
	eviction := Action{Class: UpstreamWrite, Verb: "create", Group: "core", Kind: "pods/eviction"}
	got, _ = allow("core", "pods").Decide(eviction)
	assert.Equal(t, Prompted, got, "a create of pods is not an eviction")
	got, _ = allow("core", "pods*").Decide(eviction)
	assert.Equal(t, Prompted, got, "a glob with no / stops at the resource")
	got, _ = allow("core", "*").Decide(eviction)
	assert.Equal(t, Allowed, got, "a bare * is every kind, subresources included")
	deny := Policy{Mode: Auto, Rules: []Rule{{ID: "r", Effect: Deny, Class: UpstreamWrite, Kind: "*"}}}
	got, _ = deny.Decide(eviction)
	assert.Equal(t, Denied, got, "a Deny of * reaches an eviction")
	got, _ = allow("apps", "deploy*").Decide(scale)
	assert.Equal(t, Allowed, got, "a glob with no / still covers the scale")
	got, _ = allow("core", "pods/*").Decide(eviction)
	assert.Equal(t, Allowed, got, "a glob with a / reaches subresources")
	got, _ = allow("core", "pods/eviction").Decide(eviction)
	assert.Equal(t, Allowed, got, "a rule that names the subresource covers it")
	deploy := Action{Class: UpstreamWrite, Verb: "patch", Group: "apps", Kind: "deployments"}
	got, _ = allow("apps", "deployments/scale").Decide(deploy)
	assert.Equal(t, Prompted, got, "a subresource does not cover its resource")

	got, _ = allow("", "").Decide(deploy)
	assert.Equal(t, Allowed, got, "an unset field matches anything")
	scoped := Policy{Mode: Ask, Rules: []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Context: "prod"}}}
	got, _ = scoped.Decide(patch(UpstreamWrite))
	assert.Equal(t, Prompted, got, "a set field refuses another value")
}

func TestASetNamespaceSkipsAClusterScopedWrite(t *testing.T) {
	node := Action{Class: UpstreamWrite, Context: "dev", Verb: "patch", Group: "core", Kind: "nodes"}
	anyNamespace := Policy{Mode: Ask, Rules: []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Context: "dev", Namespace: "*"}}}
	got, _ := anyNamespace.Decide(node)
	assert.Equal(t, Prompted, got, "* is every namespace, not none")
	got, _ = anyNamespace.Decide(patch(UpstreamWrite))
	assert.Equal(t, Allowed, got)

	unset := Policy{Mode: Ask, Rules: []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Context: "dev"}}}
	got, _ = unset.Decide(node)
	assert.Equal(t, Allowed, got, "an unset namespace still covers a cluster-scoped write")
}

func TestARuleRoundTripsThroughJSON(t *testing.T) {
	rule := Rule{ID: "r1", Effect: Deny, Class: UpstreamWrite, Context: "prod-eu", Namespace: "team-a", Verb: "delete", Group: "core", Kind: "pods"}
	b, err := json.Marshal(rule)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"r1","effect":"deny","class":4,"context":"prod-eu","namespace":"team-a","verb":"delete","group":"core","kind":"pods"}`, string(b))

	var back Rule
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r1","effect":"allow","class":4}`), &back))
	b, err = json.Marshal(back)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"r1","effect":"allow","class":4}`, string(b), "an unset field is left out")
}

func TestARuleReadsAsALine(t *testing.T) {
	for want, rule := range map[string]Rule{
		"Allow cluster writes in dev-eks / team-a":              {Effect: Allow, Class: UpstreamWrite, Context: "dev-eks", Namespace: "team-a"},
		"Deny destructive delete of core namespaces in prod-eu": {Effect: Deny, Class: Destructive, Context: "prod-eu", Verb: "delete", Group: "core", Kind: "namespaces"},
		"Ask for destructive cluster writes everywhere":         {Effect: AskFor, Class: Destructive},
		"Deny Secret reads in any context / kube-system":        {Effect: Deny, Class: SecretRead, Namespace: "kube-system"},
		"Deny delete of anything everywhere":                    {Effect: Deny, Class: UpstreamWrite, Verb: "delete"},
		"Deny writes of apps resources everywhere":              {Effect: Deny, Class: UpstreamWrite, Group: "apps"},
		"Deny writes of deployments everywhere":                 {Effect: Deny, Class: UpstreamWrite, Kind: "deployments"},
		"Deny destructive writes of deployments everywhere":     {Effect: Deny, Class: Destructive, Kind: "deployments"},
	} {
		assert.Equal(t, want, rule.Line())
	}
}

func TestAReasonSaysWhatDecided(t *testing.T) {
	for want, c := range map[string]struct {
		policy Policy
		act    Action
	}{
		"it changes nothing in the cluster": {Policy{Mode: ReadOnly}, patch(ReadInside)},
		"it always asks":                    {Policy{Mode: Auto}, patch(Destructive)},
		"auto mode":                         {Policy{Mode: Auto}, patch(UpstreamWrite)},
		"ask mode":                          {Policy{Mode: Ask}, patch(UpstreamWrite)},
		"this context is read-only":         {Policy{Mode: ReadOnly}, patch(UpstreamWrite)},
	} {
		_, why := c.policy.Decide(c.act)
		assert.Equal(t, want, why)
	}
}

func TestTheZeroVerdictIsADenial(t *testing.T) {
	var v Verdict
	assert.Equal(t, Unmatched, v)
	assert.Less(t, Unmatched, Permit)
	assert.Less(t, Permit, Forbid)
	assert.Less(t, Forbid, Refuse)
}

func TestOutcomeOfEveryVerdict(t *testing.T) {
	for v, want := range map[Verdict]Decision{
		Permit:    Allowed,
		Unmatched: Prompted,
		Forbid:    Prompted,
		Refuse:    Denied,
	} {
		assert.Equal(t, want, v.Outcome(), "verdict %d", v)
	}
}

// orderedTable states the policy as a priority list: the first branch that
// applies decides. It is a second statement of the same policy as Authorize,
// so a change to the table changes this list in the same commit.
func orderedTable(p Policy, act Action) (Decision, string) {
	if act.Class == ReadInside || act.Class == WriteInside {
		return Allowed, "it changes nothing in the cluster"
	}
	if r, ok := p.first(Deny, act); ok {
		return Denied, "a rule denies it: " + r.Line()
	}
	if p.Mode == ReadOnly && act.Class != SecretRead {
		return Denied, "this context is read-only"
	}
	if r, ok := p.first(AskFor, act); ok {
		return Prompted, "a rule asks for it: " + r.Line()
	}
	if act.Class == Destructive {
		return Prompted, "it always asks"
	}
	if r, ok := p.first(Allow, act); ok {
		return Allowed, "a rule allows it: " + r.Line()
	}
	if p.Mode == Auto {
		return Allowed, "auto mode"
	}
	return Prompted, string(p.Mode) + " mode"
}

func TestTheVerdictMatchesTheOrderedTable(t *testing.T) {
	// Each effect is absent, or one rule of class 4 or 5: the classes a rule
	// can have.
	choices := []Class{0, UpstreamWrite, Destructive}
	for _, mode := range []Mode{ReadOnly, Ask, Auto} {
		for class := ReadInside; class <= SecretRead; class++ {
			for _, deny := range choices {
				for _, ask := range choices {
					for _, allow := range choices {
						var rules []Rule
						for e, c := range map[Effect]Class{Deny: deny, AskFor: ask, Allow: allow} {
							if c != 0 {
								rules = append(rules, Rule{ID: string(e), Effect: e, Class: c})
							}
						}
						p := Policy{Mode: mode, Rules: rules}
						want, wantWhy := orderedTable(p, patch(class))
						v, why := p.Authorize(patch(class))
						assert.Equal(t, want, v.Outcome(), "%s, class %d, rules %v", mode, class, rules)
						assert.Equal(t, wantWhy, why, "%s, class %d, rules %v", mode, class, rules)
					}
				}
			}
		}
	}
}

func TestAForbidOnAFolderClassRefuses(t *testing.T) {
	// No rule Kstack reads today has class 1: the shape checks refuse one.
	// Should one reach the engine anyway, a forbid still wins.
	for _, c := range []struct {
		effect Effect
		want   Verdict
		why    string
	}{
		{Deny, Refuse, "a rule denies it: Deny reads inside the sandbox everywhere"},
		{AskFor, Forbid, "a rule asks for it: Ask for reads inside the sandbox everywhere"},
		{Allow, Permit, "it changes nothing in the cluster"},
	} {
		rules := []Rule{{ID: "r", Effect: c.effect, Class: ReadInside}}
		got, why := Policy{Mode: Auto, Rules: rules}.Authorize(patch(ReadInside))
		assert.Equal(t, c.want, got, "%s", c.effect)
		assert.Equal(t, c.why, why, "%s", c.effect)
	}
}

func TestAVerdictIsOrderIndependent(t *testing.T) {
	rule := func(e Effect) Rule { return Rule{ID: string(e), Effect: e, Class: UpstreamWrite} }
	for _, rules := range [][]Rule{
		{rule(Deny), rule(AskFor), rule(Allow)},
		{rule(Deny), rule(Allow), rule(AskFor)},
		{rule(AskFor), rule(Deny), rule(Allow)},
		{rule(AskFor), rule(Allow), rule(Deny)},
		{rule(Allow), rule(Deny), rule(AskFor)},
		{rule(Allow), rule(AskFor), rule(Deny)},
	} {
		got, why := Policy{Mode: Auto, Rules: rules}.Authorize(patch(UpstreamWrite))
		assert.Equal(t, Refuse, got, "%v", rules)
		assert.Equal(t, "a rule denies it: Deny cluster writes everywhere", why, "%v", rules)
	}
}

func TestAForbidWinsOverAPermit(t *testing.T) {
	rule := func(e Effect) Rule { return Rule{ID: string(e), Effect: e, Class: UpstreamWrite} }
	got, _ := Policy{Mode: Ask, Rules: []Rule{rule(Allow), rule(AskFor)}}.Authorize(patch(UpstreamWrite))
	assert.Equal(t, Forbid, got)
	got, _ = Policy{Mode: Ask, Rules: []Rule{rule(Allow), rule(Deny)}}.Authorize(patch(UpstreamWrite))
	assert.Equal(t, Refuse, got)
	got, _ = Policy{Mode: Auto, Rules: []Rule{rule(Allow)}}.Authorize(patch(Destructive))
	assert.Equal(t, Forbid, got)
}

func TestAnUnmatchedActionIsTheDefaultDenial(t *testing.T) {
	got, why := Policy{Mode: Ask}.Authorize(patch(UpstreamWrite))
	assert.Equal(t, Unmatched, got)
	assert.Equal(t, Prompted, got.Outcome())
	assert.Equal(t, "ask mode", why)
	got, _ = Policy{Mode: Auto}.Authorize(patch(UpstreamWrite))
	assert.Equal(t, Permit, got)
}

func TestReadOnlyRefusesClassesThreeToFive(t *testing.T) {
	for class, want := range map[Class]Verdict{
		ReadInside:    Permit,
		WriteInside:   Permit,
		NewHost:       Refuse,
		UpstreamWrite: Refuse,
		Destructive:   Refuse,
		SecretRead:    Unmatched,
	} {
		got, _ := Policy{Mode: ReadOnly}.Authorize(patch(class))
		assert.Equal(t, want, got, "class %d", class)
	}
}
