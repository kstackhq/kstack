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

package logsview

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustertest"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// clock is the tool's now in every test: 2026-10-08T14:04:00Z.
var clock = time.Date(2026, 10, 8, 14, 4, 0, 0, time.UTC)

// fakeService is the cluster service as the tool reads it: one reading handed
// to read, the active cache's health, and each statement's answer by what it asks.
type fakeService struct {
	cluster.Service
	active  cluster.ActiveCluster
	readErr error
	health  cluster.ClusterCacheHealth
	// answer answers a statement; nil answers no cache.
	answer func(q queried) cluster.ClusterCachedDataQueryResult
	// queryErr fails every statement.
	queryErr error
	// queried is every statement run.
	queried []queried
}

// queried is one statement a fake ran, and the args bound to it.
type queried struct {
	sql  string
	args []any
}

type (
	fakeClusters struct {
		cluster.Clusters
		*fakeService
	}
	fakeCaches struct {
		cluster.Caches
		*fakeService
	}
	fakeCachedData struct {
		cluster.CachedData
		*fakeService
	}
)

func (f *fakeService) Clusters() cluster.Clusters     { return fakeClusters{fakeService: f} }
func (f *fakeService) Caches() cluster.Caches         { return fakeCaches{fakeService: f} }
func (f *fakeService) CachedData() cluster.CachedData { return fakeCachedData{fakeService: f} }

func (f fakeClusters) ReadActive(ctx context.Context, _ cluster.ClusterID, read func(context.Context, cluster.ActiveCluster) error) error {
	if f.readErr != nil {
		return f.readErr
	}
	return read(ctx, f.active)
}

func (f fakeCachedData) ReadActive(ctx context.Context, id cluster.ClusterID, maxRows, maxBytes int, read func(cluster.CacheReading, cluster.ActiveCluster) error) error {
	return cluster.ReadActiveCache(ctx, f.fakeService, id, maxRows, maxBytes, read)
}

func (f fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return f.health, true, nil
}

func (f fakeCachedData) QueryOwn(_ context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, sql string, _, _ int, args ...any) (cluster.ClusterCachedDataQueryResult, bool, error) {
	q := queried{sql, args}
	f.queried = append(f.queried, q)
	if f.queryErr != nil {
		return cluster.ClusterCachedDataQueryResult{}, false, f.queryErr
	}
	if f.answer == nil {
		return cluster.ClusterCachedDataQueryResult{}, false, nil
	}
	return f.answer(q), true, nil
}

// watching is a cluster whose cache is watching, holding webappObjects.
func watching(t *testing.T) *fakeService {
	t.Helper()
	return mirrorOf(t, webappObjects()...)
}

// run calls the tool over f with raw, for cluster 7, and answers what the
// model reads and the action.
func run(t *testing.T, f *fakeService, raw string) (string, bool, *tools.Action) {
	t.Helper()
	return New(f, func() time.Time { return clock }).RunShown(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(raw))
}

// viewOf is the view a call over f opens, which must open.
func viewOf(t *testing.T, f *fakeService, raw string) (string, tools.LogsViewAction) {
	t.Helper()
	text, isError, shown := run(t, f, raw)
	require.False(t, isError, text)
	require.NotNil(t, shown)
	require.NotNil(t, shown.LogsView)
	return text, *shown.LogsView
}

// refusalOf is the refusal a call over f answers, which must refuse with no action.
func refusalOf(t *testing.T, f *fakeService, raw string) string {
	t.Helper()
	text, isError, shown := run(t, f, raw)
	require.True(t, isError, text)
	assert.Nil(t, shown)
	return text
}

const webapp = `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`

func TestTheDefinitionIsTheEmbeddedSchema(t *testing.T) {
	def := New(nil, time.Now).Definition()

	assert.Equal(t, Name, def.Name)
	assert.Equal(t, description, def.Description)
	assert.JSONEq(t, string(inputSchema), string(def.InputSchema))
}

func TestTheToolIsOfKindLogsView(t *testing.T) {
	tool := New(nil, time.Now)

	assert.Equal(t, Name, tool.Name())
	assert.Equal(t, tools.ActionLogsView, tool.ActionKind())
	assert.Equal(t, prompt, tool.Prompt())
}

// The view is the run's: read off its arguments a call shows no action, so a
// call that never opened one shows its kind alone.
func TestACallShowsNoActionOffItsArguments(t *testing.T) {
	_, err := New(nil, time.Now).Action(json.RawMessage(webapp), "", false)
	require.ErrorIs(t, err, errNotShown)
}

func TestASourceInTheMirrorOpens(t *testing.T) {
	f := watching(t)
	text, view := viewOf(t, f, webapp)

	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Defaulted container app out of app, istio-proxy, setup.", text)
	assert.Equal(t, tools.LogsViewAction{
		LogsViewInput: tools.LogsViewInput{
			Sources: []tools.LogsViewSource{{Namespace: "prod", Kind: tools.LogsSourceDeployment, Name: "webapp", Containers: []string{}}},
			Filters: []tools.LogsViewFilter{},
		},
		Anchor: tools.LogsViewAnchor{Kind: tools.LogsAnchorTail},
	}, view)
	assert.Contains(t, f.queried[0].sql, "o.api_version = ? AND o.kind = ? AND o.namespace = ? AND o.name = ?")
	assert.Equal(t, []any{"apps/v1", "Deployment", "prod", "webapp"}, f.queried[0].args)
}

// A source the mirror does not hold yet opens, so a view can wait for a pod with
// a fixed name or a workload not yet created: with no container named it reads
// each pod's default once one appears, and the receipt says it is not there yet.
func TestASourceNotInTheMirrorYetOpens(t *testing.T) {
	text, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/gone"}]}`)
	assert.Equal(t, []string{}, view.Sources[0].Containers)
	assert.False(t, view.Sources[0].AllContainers)
	assert.Equal(t, "Viewing pods/gone in prod at the newest line. Default container. Not in Kstack's cache of the cluster yet; the view picks it up when it appears.", text)

	text, view = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/gone","all_containers":true}]}`)
	assert.True(t, view.Sources[0].AllContainers)
	assert.Equal(t, "Viewing pods/gone in prod at the newest line. Every container. Not in Kstack's cache of the cluster yet; the view picks it up when it appears.", text)

	text, view = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/later","containers":["app"],"previous":true}]}`)
	assert.Equal(t, []string{"app"}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing deployments/later in prod at the newest line. Container app. Previous instance. Not in Kstack's cache of the cluster yet; the view picks it up when it appears.", text)
}

func TestAKindTheBackendDoesNotReadIsRefused(t *testing.T) {
	text := refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"services/webapp"}]}`)
	assert.Contains(t, text, `"error":"bad-input"`)
	assert.Contains(t, text, `"field":"resource"`)
	assert.Contains(t, text, `"source":0`)
	assert.NotContains(t, text, "services")
}

// A resource is read as kubectl spells one: plural, singular or short.
func TestAResourceIsReadAsKubectlSpellsIt(t *testing.T) {
	for _, resource := range []string{"deployments/webapp", "deployment/webapp", "deploy/webapp", "Deployments/webapp"} {
		_, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"`+resource+`"}]}`)
		assert.Equal(t, tools.LogsSourceDeployment, view.Sources[0].Kind, resource)
	}
}

func TestAContainerNoPodHasIsRefused(t *testing.T) {
	text := refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","containers":["app","nope"]}]}`)
	assert.JSONEq(t, `{"error":"no-container","source":0,"containers":["app","istio-proxy","setup"]}`, text)
}

// With no container named the view leaves the default to the backend, which
// picks it per pod; the receipt names the one the object has now, the
// annotation's else the first, beside the others, and a one-container pod reads
// no such line.
func TestNoContainerNamedLeavesTheDefaultToEachPod(t *testing.T) {
	f := watching(t)
	text, view := viewOf(t, f, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, []string{}, view.Sources[0].Containers)
	assert.False(t, view.Sources[0].AllContainers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Defaulted container app out of app, istio-proxy, setup.", text)

	annotated := mirrorOf(t, podOf("webapp-7f9c", "istio-proxy", nil, container{name: "app"}, container{name: "istio-proxy"}))
	text, _ = viewOf(t, annotated, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Defaulted container istio-proxy out of app, istio-proxy.", text)

	one := mirrorOf(t, podOf("webapp-7f9c", "", nil, container{name: "app"}))
	text, view = viewOf(t, one, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, []string{}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line.", text)
}

func TestAllContainersIsSetOnTheSource(t *testing.T) {
	text, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","all_containers":true}]}`)
	assert.Equal(t, []string{}, view.Sources[0].Containers)
	assert.True(t, view.Sources[0].AllContainers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers app, istio-proxy, setup.", text)

	text = refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","all_containers":true,"containers":["app"]}]}`)
	assert.JSONEq(t, `{"error":"bad-input","field":"all_containers","source":0,"message":"not with containers"}`, text)
}

func TestTheContainersNamedAreShown(t *testing.T) {
	text, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","containers":["istio-proxy","app"]}]}`)
	assert.Equal(t, []string{"istio-proxy", "app"}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers istio-proxy, app.", text)
}

// previous on a pod reads the shown containers that restarted, and with none the
// view waits for one; on a workload the receipt counts the pods.
func TestPreviousNeedsARestartedContainer(t *testing.T) {
	text, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true}]}`)
	assert.True(t, view.Sources[0].Previous)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Defaulted container app out of app, istio-proxy, setup. Previous instance.", text)

	text, _ = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true,"containers":["app","istio-proxy"]}]}`)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers app, istio-proxy. Previous instance.", text)

	text, _ = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true,"containers":["istio-proxy"]}]}`)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Container istio-proxy. Previous instance. Nothing to read yet; the view picks up its containers as they restart.", text)

	text, _ = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp","previous":true}]}`)
	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Defaulted container app out of app, istio-proxy, setup. Previous instance of 2 of 3 pods.", text)

	text, _ = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp","previous":true,"containers":["istio-proxy","app"]}]}`)
	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Containers istio-proxy, app. Previous instance of 2 of 3 pods.", text)
}

// A workload's view follows its pods as they come and go, so it opens with none
// yet, none running a container its template has, or none restarted, and the
// receipt says it has nothing to read yet.
func TestAWorkloadOpensWhateverItsPodsAre(t *testing.T) {
	const webapp = `{"namespace":"prod","resource":"deployments/webapp"`
	const idle = " Nothing to read yet; the view picks up its containers as they start."
	objects := webappObjects()

	text, _ := viewOf(t, mirrorOf(t, objects[:2]...), `{"sources":[`+webapp+`}]}`)
	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Defaulted container app out of app, istio-proxy, setup."+idle, text)

	text, view := viewOf(t, mirrorOf(t, append(objects[:2:2], podOf("webapp-c", "", webappReplicaSet(), container{name: "web"}))...), `{"sources":[`+webapp+`,"containers":["app"]}]}`)
	assert.Equal(t, []string{"app"}, view.Sources[0].Containers)
	assert.True(t, strings.HasSuffix(text, idle), text)

	text, _ = viewOf(t, mirrorOf(t, append(objects[:2:2], podOf("webapp-c", "", webappReplicaSet(), container{name: "app"}))...), `{"sources":[`+webapp+`,"previous":true}]}`)
	assert.True(t, strings.HasSuffix(text, " Previous instance of 0 of 1 pods. Nothing to read yet; the view picks up its containers as they restart."), text)

	text, _ = viewOf(t, watching(t), `{"sources":[`+webapp+`}]}`)
	assert.NotContains(t, text, "Nothing to read")
}

// A view the logs service refuses is refused by code: one past the cap, and a
// source whose kind the cache does not mirror, or a cache mirroring no pods,
// since nothing could ever appear to follow.
func TestAViewTheServiceRefusesIsRefused(t *testing.T) {
	many := webappObjects()[:2:2]
	for i := range 101 {
		many = append(many, podOf("webapp-"+strconv.Itoa(i), "", webappReplicaSet(), container{name: "app"}))
	}
	assert.JSONEq(t, `{"error":"too-many"}`, refusalOf(t, mirrorOf(t, many...), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`))

	assert.JSONEq(t, `{"error":"not-mirrored","source":1}`, refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"},{"namespace":"prod","resource":"jobs/nightly"}]}`))

	s := clustertest.NewStore(t)
	s.Apply(t, webappObjects()[0])
	assert.JSONEq(t, `{"error":"not-mirrored"}`, refusalOf(t, over(t, s), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`))
}

func TestAGrepThatDoesNotCompileIsRefused(t *testing.T) {
	text := refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"grep":"("}`)
	assert.JSONEq(t, `{"error":"bad-input","field":"grep","message":"does not compile as a regular expression"}`, text)
}

func TestTheAnchorIsReadOnTheClock(t *testing.T) {
	at := func(s string) *time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		v = v.UTC()
		return &v
	}
	for anchor, want := range map[string]tools.LogsViewAnchor{
		`"head"`:                      {Kind: tools.LogsAnchorHead},
		`"tail"`:                      {Kind: tools.LogsAnchorTail},
		`""`:                          {Kind: tools.LogsAnchorTail},
		`"2026-10-08T13:00:00+01:00"`: {Kind: tools.LogsAnchorAt, At: at("2026-10-08T12:00:00Z")},
		`"2m"`:                        {Kind: tools.LogsAnchorAt, At: at("2026-10-08T14:02:00Z")},
	} {
		_, view := viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"anchor":`+anchor+`}`)
		assert.Equal(t, want, view.Anchor, anchor)
	}
	_, view := viewOf(t, watching(t), webapp)
	assert.Equal(t, tools.LogsViewAnchor{Kind: tools.LogsAnchorTail}, view.Anchor, "absent is tail")
}

func TestAnUnreadableAnchorIsRefused(t *testing.T) {
	for _, anchor := range []string{`"yesterday"`, `"-2m"`, `"0s"`, `3`} {
		text := refusalOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"anchor":`+anchor+`}`)
		assert.Contains(t, text, `"field":"anchor"`, anchor)
		assert.NotContains(t, text, "yesterday")
	}
}

func TestPinToEndAbsentIsFalse(t *testing.T) {
	_, view := viewOf(t, watching(t), webapp)
	assert.False(t, view.PinToEnd)
	_, view = viewOf(t, watching(t), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"pin_to_end":true}`)
	assert.True(t, view.PinToEnd)
}

// The receipt with everything set: every source and its note, the anchor, the
// pin, and the grep.
func TestTheReceiptSpellsTheWholeView(t *testing.T) {
	text, view := viewOf(t, watching(t), `{"description":"Why it crashed","sources":[
		{"namespace":"prod","resource":"deployments/webapp","previous":true},
		{"namespace":"prod","resource":"pods/webapp-7f9c","containers":["app","istio-proxy"]}],
		"filters":{"zone":["eu-west-1a"],"node":["ip-10-0-1-5"]},"grep":"error|panic","anchor":"head","pin_to_end":true}`)

	assert.Equal(t, "Viewing deployments/webapp in prod and 1 more source from the start, pinned to the end. "+
		"deployments/webapp: defaulted container app out of app, istio-proxy, setup. deployments/webapp: previous instance of 2 of 3 pods. "+
		"pods/webapp-7f9c: containers app, istio-proxy. Matching /error|panic/.", text)
	assert.Equal(t, []tools.LogsViewFilter{{Field: tools.LogsFilterNode, Values: []string{"ip-10-0-1-5"}}, {Field: tools.LogsFilterZone, Values: []string{"eu-west-1a"}}},
		view.Filters, "in the action's order, whatever the call's")
	assert.Equal(t, "error|panic", view.Grep)
	require.Len(t, view.Sources, 2)
	assert.Equal(t, tools.LogsViewSource{Namespace: "prod", Kind: tools.LogsSourcePod, Name: "webapp-7f9c", Containers: []string{"app", "istio-proxy"}}, view.Sources[1])
}

// A pod's previous instance is named alone, and more than one more source is counted.
func TestTheReceiptCountsTheSources(t *testing.T) {
	text, _ := viewOf(t, watching(t), `{"sources":[
		{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true},
		{"namespace":"prod","resource":"deployments/webapp"},
		{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)

	assert.True(t, strings.HasPrefix(text, "Viewing pods/webapp-7f9c in prod and 2 more sources"), text)
	assert.Contains(t, text, "pods/webapp-7f9c: previous instance.")
}

func TestTheActionCarriesTheDescription(t *testing.T) {
	_, _, shown := run(t, watching(t), `{"description":"Tail the app","sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`)
	require.NotNil(t, shown)
	assert.Equal(t, "Tail the app", shown.Description)
}

// A key the tool does not know is refused without naming it, so nothing the
// model wrote comes back to it; a name that is not a DNS name is refused by its field.
func TestBadArgumentsAreRefusedByField(t *testing.T) {
	for raw, want := range map[string]string{
		`{"sources":[{"namespace":"prod","resource":"pods/a","hunter2":1}]}`: `{"error":"bad-input","source":0}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"hunter2":1}`: `{"error":"bad-input"}`,
		`{"sources":[]}`: `{"error":"bad-input","field":"sources"}`,
		`{}`:             `{"error":"bad-input","field":"sources"}`,
		`{"sources":[{"namespace":"Prod","resource":"pods/a"}]}`:                         `{"error":"bad-input","field":"namespace","source":0}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a","containers":["A"]}]}`:      `{"error":"bad-input","field":"containers","source":0}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a","previous":null}]}`:         `{"error":"bad-input","field":"previous","source":0}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"node":[]}}`:   `{"error":"bad-input","field":"filters.node","message":"never empty"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"pod":["a"]}}`: `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"grep":"a\nb"}`:           `{"error":"bad-input","field":"grep"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}]} {}`:                      `{"error":"bad-input"}`,
		`[]`:             `{"error":"bad-input"}`,
		`{"sources":{}}`: `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"","resource":"pods/a"}]}`:                             `{"error":"bad-input","field":"namespace","source":0}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"grep":"a","grep":"b"}`:   `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":[]}`:            `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"node":"a"}}`:  `{"error":"bad-input","field":"filters.node"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"node":[1]}}`:  `{"error":"bad-input","field":"filters.node"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"node":[""]}}`: `{"error":"bad-input","field":"filters.node","message":"never empty"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}]`:                          `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}`:                           `{"error":"bad-input"}`,
		`{"sources":[{"namespace":"prod","resource":"pods/a"}],"filters":{"node":["a"`:   `{"error":"bad-input","field":"filters.node"}`,
	} {
		text := refusalOf(t, watching(t), raw)
		assert.JSONEq(t, want, text, raw)
		assert.NotContains(t, text, "hunter2", raw)
	}
}

func TestNoCacheIsARefusal(t *testing.T) {
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, &fakeService{readErr: cluster.ErrNotFound}, webapp))

	none := watching(t)
	none.active.Cache = nil
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, none, webapp))

	gone := watching(t)
	gone.answer = nil
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, gone, webapp))
}

func TestASyncingCacheIsARefusal(t *testing.T) {
	f := watching(t)
	f.health.Reason = "Connecting"
	assert.JSONEq(t, `{"error":"syncing"}`, refusalOf(t, f, webapp))
}

func TestAReadFailureCarriesNoText(t *testing.T) {
	f := watching(t)
	f.queryErr = errors.New("open cache 7: disk I/O error at /home/user")
	text := refusalOf(t, f, webapp)
	assert.JSONEq(t, `{"error":"read-failed"}`, text)
}

// Run is RunShown with the action dropped.
func TestRunAnswersTheReceipt(t *testing.T) {
	text, isError := New(watching(t), func() time.Time { return clock }).Run(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(webapp))
	assert.False(t, isError)
	assert.True(t, strings.HasPrefix(text, "Viewing deployments/webapp in prod"), text)
}
