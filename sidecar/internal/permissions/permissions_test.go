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

// k8s is a Kubernetes action of class c in context dev, namespace team-a.
func k8s(c Class) Action {
	return Action{
		Provider: Kubernetes, Class: c, Scope: Scope{Context: "dev", Namespace: "team-a"},
		Verb: "patch", Group: "apps", Kind: "deployments", Name: "api",
	}
}

// The note's table, with Trusted read as Ask plus an Allow rule over the scope.
func TestDecideFollowsTheModeTable(t *testing.T) {
	trusted := []Rule{{ID: "t", Effect: Allow, Class: UpstreamWrite, Provider: Kubernetes, Scope: Scope{Context: "dev", Namespace: "team-a"}}}
	type row struct {
		mode  Mode
		rules []Rule
		want  map[Class]Decision
	}
	for name, r := range map[string]row{
		"read-only": {ReadOnly, nil, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Denied, UpstreamWrite: Denied, Destructive: Denied, SecretRead: Prompted,
		}},
		"ask": {Ask, nil, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Prompted, UpstreamWrite: Prompted, Destructive: Prompted, SecretRead: Prompted,
		}},
		"trusted": {Ask, trusted, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Prompted, UpstreamWrite: Allowed, Destructive: Prompted, SecretRead: Prompted,
		}},
		"auto": {Auto, nil, map[Class]Decision{
			ReadInside: Allowed, WriteInside: Allowed, NewHost: Allowed, UpstreamWrite: Allowed, Destructive: Prompted, SecretRead: Allowed,
		}},
	} {
		for class, want := range r.want {
			act := k8s(class)
			if class == NewHost {
				act = Action{Provider: Net, Class: NewHost, Scope: Scope{Host: "example.com"}, Verb: "CONNECT", Kind: "443"}
			}
			got, _ := Decide(Policy{Mode: r.mode}, r.rules, act)
			assert.Equal(t, want, got, "%s, class %d", name, class)
		}
	}
}

func TestClassFiveAsksInEveryMode(t *testing.T) {
	allow := []Rule{{ID: "a", Effect: Allow, Class: UpstreamWrite, Provider: Kubernetes}}
	for _, mode := range []Mode{Ask, Auto} {
		for _, rules := range [][]Rule{nil, allow} {
			got, why := Decide(Policy{Mode: mode}, rules, k8s(Destructive))
			assert.Equal(t, Prompted, got, "%s", mode)
			assert.Equal(t, "it always asks", why.String())
		}
	}
	got, why := Decide(Policy{Mode: ReadOnly}, allow, k8s(Destructive))
	assert.Equal(t, Denied, got)
	assert.Equal(t, "this context is read-only", why.String())
}

func TestDenyWinsOverAskWinsOverAllow(t *testing.T) {
	rule := func(e Effect) Rule { return Rule{ID: string(e), Effect: e, Class: UpstreamWrite, Provider: Kubernetes} }
	for _, c := range []struct {
		rules []Rule
		want  Decision
		rule  string
	}{
		{[]Rule{rule(Allow), rule(AskFor), rule(Deny)}, Denied, "deny"},
		{[]Rule{rule(Allow), rule(AskFor)}, Prompted, "ask"},
		{[]Rule{rule(Allow)}, Allowed, "allow"},
	} {
		got, why := Decide(Policy{Mode: Auto}, c.rules, k8s(UpstreamWrite))
		assert.Equal(t, c.want, got)
		require.NotNil(t, why.Rule)
		assert.Equal(t, c.rule, why.Rule.ID)
	}
}

func TestAClassFourRuleCoversClassFive(t *testing.T) {
	scope := Scope{Context: "dev", Namespace: "team-a"}
	for _, c := range []struct {
		effect Effect
		want   Decision
	}{{Deny, Denied}, {AskFor, Prompted}, {Allow, Prompted}} {
		rules := []Rule{{ID: "r", Effect: c.effect, Class: UpstreamWrite, Provider: Kubernetes, Scope: scope}}
		got, _ := Decide(Policy{Mode: Auto}, rules, k8s(Destructive))
		assert.Equal(t, c.want, got, "%s", c.effect)
	}
	rules := []Rule{{ID: "r", Effect: Deny, Class: Destructive, Provider: Kubernetes}}
	got, _ := Decide(Policy{Mode: Auto}, rules, k8s(UpstreamWrite))
	assert.Equal(t, Allowed, got, "a class 5 rule leaves class 4 alone")
}

func TestARuleMatchesByGroupAndSubresource(t *testing.T) {
	allow := func(group, kind string) []Rule {
		return []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Provider: Kubernetes, Group: group, Kind: kind}}
	}
	pods := Action{Provider: Kubernetes, Class: UpstreamWrite, Verb: "delete", Group: "example.com", Kind: "pods"}
	got, _ := Decide(Policy{Mode: Ask}, allow("core", "pods"), pods)
	assert.Equal(t, Prompted, got, "core pods is not another group's pods")
	pods.Group = "core"
	got, _ = Decide(Policy{Mode: Ask}, allow("core", "pods"), pods)
	assert.Equal(t, Allowed, got)

	scale := Action{Provider: Kubernetes, Class: UpstreamWrite, Verb: "patch", Group: "apps", Kind: "deployments/scale"}
	got, _ = Decide(Policy{Mode: Ask}, allow("apps", "deployments"), scale)
	assert.Equal(t, Allowed, got, "a resource covers its scale")
	status := Action{Provider: Kubernetes, Class: UpstreamWrite, Verb: "update", Group: "apps", Kind: "deployments/status"}
	got, _ = Decide(Policy{Mode: Ask}, allow("apps", "deployments"), status)
	assert.Equal(t, Prompted, got, "a resource does not cover its other subresources")
	eviction := Action{Provider: Kubernetes, Class: UpstreamWrite, Verb: "create", Group: "core", Kind: "pods/eviction"}
	got, _ = Decide(Policy{Mode: Ask}, allow("core", "pods"), eviction)
	assert.Equal(t, Prompted, got, "a create of pods is not an eviction")
	got, _ = Decide(Policy{Mode: Ask}, allow("core", "pods/eviction"), eviction)
	assert.Equal(t, Allowed, got, "a rule that names the subresource covers it")
	deploy := Action{Provider: Kubernetes, Class: UpstreamWrite, Verb: "patch", Group: "apps", Kind: "deployments"}
	got, _ = Decide(Policy{Mode: Ask}, allow("apps", "deployments/scale"), deploy)
	assert.Equal(t, Prompted, got, "a subresource does not cover its resource")

	got, _ = Decide(Policy{Mode: Ask}, allow("", ""), deploy)
	assert.Equal(t, Allowed, got, "an unset field matches anything")
	scoped := []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Provider: Kubernetes, Scope: Scope{Context: "prod"}}}
	got, _ = Decide(Policy{Mode: Ask}, scoped, k8s(UpstreamWrite))
	assert.Equal(t, Prompted, got, "a set field refuses another value")
	net := []Rule{{ID: "r", Effect: Allow, Class: UpstreamWrite, Provider: Net}}
	got, _ = Decide(Policy{Mode: Ask}, net, k8s(UpstreamWrite))
	assert.Equal(t, Prompted, got, "a rule matches its own provider alone")
}

func TestNoPromptsTurnsAPromptIntoADenial(t *testing.T) {
	got, why := Decide(Policy{Mode: Ask, NoPrompts: true}, nil, k8s(UpstreamWrite))
	assert.Equal(t, Denied, got)
	assert.Equal(t, "nobody can be asked", why.String())

	got, _ = Decide(Policy{Mode: Auto, NoPrompts: true}, nil, k8s(UpstreamWrite))
	assert.Equal(t, Allowed, got)
}

func TestARuleRoundTripsThroughJSON(t *testing.T) {
	rule := Rule{
		ID: "r1", Effect: Deny, Class: UpstreamWrite, Provider: Kubernetes,
		Scope: Scope{Context: "prod-eu", Namespace: "team-a"}, Verb: "delete", Group: "core", Kind: "pods",
	}
	b, err := json.Marshal(rule)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"r1","effect":"deny","class":4,"provider":"k8s",
		"scope":{"context":"prod-eu","namespace":"team-a"},"verb":"delete","group":"core","kind":"pods"}`, string(b))

	var back Rule
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r1","effect":"allow","class":4,"provider":"k8s"}`), &back))
	assert.Equal(t, `{"id":"r1","effect":"allow","class":4,"provider":"k8s"}`, mustJSON(t, back), "an empty scope is left out")
}

func TestARuleReadsAsALine(t *testing.T) {
	assert.Equal(t, "Allow cluster writes in dev-eks / team-a",
		Rule{Effect: Allow, Class: UpstreamWrite, Provider: Kubernetes, Scope: Scope{Context: "dev-eks", Namespace: "team-a"}}.String())
	assert.Equal(t, "Deny delete of core namespaces in prod-eu",
		Rule{Effect: Deny, Class: Destructive, Provider: Kubernetes, Scope: Scope{Context: "prod-eu"}, Verb: "delete", Group: "core", Kind: "namespaces"}.String())
	assert.Equal(t, "Ask for destructive cluster writes everywhere",
		Rule{Effect: AskFor, Class: Destructive, Provider: Kubernetes}.String())
	assert.Equal(t, "Deny Secret reads in any context / kube-system",
		Rule{Effect: Deny, Class: SecretRead, Provider: Kubernetes, Scope: Scope{Namespace: "kube-system"}}.String())
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestAReasonSaysWhatDecided(t *testing.T) {
	for want, why := range map[string]Reason{
		"it changes nothing":          {Class: ReadInside},
		"it stays inside the sandbox": {Class: WriteInside},
		"it always asks":              {Class: Destructive},
		"auto mode":                   {Mode: Auto},
		"ask mode":                    {Mode: Ask},
		"this context is read-only":   {Mode: ReadOnly},
		"nobody can be asked":         {NoPrompts: true, Mode: Ask},
		"":                            {},
	} {
		assert.Equal(t, want, why.String())
	}
	got, why := Decide(Policy{Mode: Ask}, nil, k8s(WriteInside))
	assert.Equal(t, Allowed, got)
	assert.Equal(t, "it stays inside the sandbox", why.String())
}

func TestARuleOfAnotherProviderReadsItsScope(t *testing.T) {
	assert.Equal(t, "Allow new hosts to example.com",
		Rule{Effect: Allow, Class: NewHost, Provider: Net, Scope: Scope{Host: "example.com"}}.String())
	assert.Equal(t, "Deny writes inside the sandbox in /srv",
		Rule{Effect: Deny, Class: WriteInside, Provider: Path, Scope: Scope{Folder: "/srv"}}.String())
	assert.Equal(t, "Deny delete of anything everywhere",
		Rule{Effect: Deny, Class: UpstreamWrite, Provider: Kubernetes, Verb: "delete"}.String())
	assert.Equal(t, "Deny writes of apps resources everywhere",
		Rule{Effect: Deny, Class: UpstreamWrite, Provider: Kubernetes, Group: "apps"}.String(), "a group alone narrows the rule")
}

func TestOnlyClusterWriteRulesAreEnforced(t *testing.T) {
	assert.True(t, Enforced(Kubernetes, UpstreamWrite))
	assert.True(t, Enforced(Kubernetes, Destructive))
	assert.False(t, Enforced(Kubernetes, SecretRead))
	assert.False(t, Enforced(Net, NewHost))
}
