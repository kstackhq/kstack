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
