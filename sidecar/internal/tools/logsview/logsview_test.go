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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// clock is the tool's now in every test: 2026-10-08T14:04:00Z.
var clock = time.Date(2026, 10, 8, 14, 4, 0, 0, time.UTC)

// fakeService is the cluster service as the tool reads it: one reading handed
// to read, the active cache's health, and each statement's answer by its text.
type fakeService struct {
	cluster.Service
	active  cluster.ActiveCluster
	readErr error
	health  cluster.ClusterCacheHealth
	// answer answers a statement; nil answers no cache.
	answer func(sql string) cluster.ClusterCachedDataQueryResult
	// queryErr fails every statement.
	queryErr error
	// queried is every statement run.
	queried []string
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

func (f fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	return f.health, true, nil
}

func (f fakeCachedData) Query(_ context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, sql string, _, _ int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	f.queried = append(f.queried, sql)
	if f.queryErr != nil {
		return cluster.ClusterCachedDataQueryResult{}, false, f.queryErr
	}
	if f.answer == nil {
		return cluster.ClusterCachedDataQueryResult{}, false, nil
	}
	return f.answer(sql), true, nil
}

// rows is a statement's answer.
func rows(r ...[]any) cluster.ClusterCachedDataQueryResult {
	return cluster.ClusterCachedDataQueryResult{Rows: r}
}

// pod is a container row of the pod read: the pod's uid and annotation, then
// the container's name and restarts.
func pod(annotated, name string, restarts int64) []any {
	var a any
	if annotated != "" {
		a = annotated
	}
	return []any{"pod-1", a, name, restarts}
}

// watching is a cluster whose cache is watching, answering each statement by
// what it asks: a pod named webapp-7f9c with app, istio-proxy and an init
// container, a deployment named webapp with the same template, and three pods
// under it, two with a restarted container.
func watching() *fakeService {
	f := &fakeService{
		active: cluster.ActiveCluster{Cluster: &cluster.Cluster{}, Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 11}}},
		health: cluster.ClusterCacheHealth{Reason: "Watching"},
	}
	f.answer = func(sql string) cluster.ClusterCachedDataQueryResult {
		switch {
		case strings.Contains(sql, "o.kind = 'Pod'") && strings.Contains(sql, "o.name = 'webapp-7f9c'"):
			return rows(pod("", "app", 2), pod("", "istio-proxy", 0), pod("", "setup", 0))
		case strings.Contains(sql, "o.kind = 'Deployment'") && strings.Contains(sql, "o.name = 'webapp'"):
			return rows([]any{"deploy-1", nil, `[{"name":"app"},{"name":"istio-proxy"}]`, `[{"name":"setup"}]`})
		case strings.Contains(sql, "ancestor_uid = 'deploy-1'"):
			return rows([]any{int64(3), int64(2)})
		}
		return rows()
	}
	return f
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
	f := watching()
	text, view := viewOf(t, f, webapp)

	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Defaulted container app out of app, istio-proxy, setup.", text)
	assert.Equal(t, tools.LogsViewAction{
		Sources: []tools.LogsViewSource{{Namespace: "prod", Kind: tools.LogsSourceDeployment, Name: "webapp", Containers: []string{"app"}}},
		Filters: []tools.LogsViewFilter{}, Anchor: tools.LogsViewAnchor{Kind: tools.LogsAnchorTail},
	}, view)
	assert.Contains(t, f.queried[0], "o.api_version = 'apps/v1' AND o.kind = 'Deployment' AND o.namespace = 'prod' AND o.name = 'webapp'")
}

func TestASourceNotInTheMirrorIsRefused(t *testing.T) {
	assert.JSONEq(t, `{"error":"not-found","source":0}`, refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/gone"}]}`))
}

func TestAKindTheBackendDoesNotReadIsRefused(t *testing.T) {
	text := refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"services/webapp"}]}`)
	assert.Contains(t, text, `"error":"bad-input"`)
	assert.Contains(t, text, `"field":"resource"`)
	assert.Contains(t, text, `"source":0`)
	assert.NotContains(t, text, "services")
}

// A resource is read as kubectl spells one: plural, singular or short.
func TestAResourceIsReadAsKubectlSpellsIt(t *testing.T) {
	for _, resource := range []string{"deployments/webapp", "deployment/webapp", "deploy/webapp", "Deployments/webapp"} {
		_, view := viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"`+resource+`"}]}`)
		assert.Equal(t, tools.LogsSourceDeployment, view.Sources[0].Kind, resource)
	}
}

func TestAContainerNoPodHasIsRefused(t *testing.T) {
	text := refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","containers":["app","nope"]}]}`)
	assert.JSONEq(t, `{"error":"no-container","source":0,"containers":["app","istio-proxy","setup"]}`, text)
}

// With no container named the annotation's is shown, else the first; the
// receipt names the others, and a one-container pod reads no such line.
func TestNoContainerNamedResolvesTheDefault(t *testing.T) {
	f := watching()
	text, view := viewOf(t, f, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, []string{"app"}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Defaulted container app out of app, istio-proxy, setup.", text)

	annotated := watching()
	annotated.answer = func(string) cluster.ClusterCachedDataQueryResult {
		return rows(pod("istio-proxy", "app", 0), pod("istio-proxy", "istio-proxy", 0))
	}
	_, view = viewOf(t, annotated, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, []string{"istio-proxy"}, view.Sources[0].Containers)

	one := watching()
	one.answer = func(string) cluster.ClusterCachedDataQueryResult { return rows(pod("", "app", 0)) }
	text, view = viewOf(t, one, `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c"}]}`)
	assert.Equal(t, []string{"app"}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line.", text)
}

func TestAllContainersWritesTheEmptyList(t *testing.T) {
	text, view := viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","all_containers":true}]}`)
	assert.Equal(t, []string{}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers app, istio-proxy, setup.", text)

	text = refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","all_containers":true,"containers":["app"]}]}`)
	assert.JSONEq(t, `{"error":"bad-input","field":"all_containers","source":0,"message":"not with containers"}`, text)
}

func TestTheContainersNamedAreShown(t *testing.T) {
	text, view := viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","containers":["istio-proxy","app"]}]}`)
	assert.Equal(t, []string{"istio-proxy", "app"}, view.Sources[0].Containers)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers istio-proxy, app.", text)
}

// previous on a pod needs a shown container that restarted; a sidecar that did
// not shows nothing and refuses nothing; on a workload the receipt counts the pods.
func TestPreviousNeedsARestartedContainer(t *testing.T) {
	text, view := viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true}]}`)
	assert.True(t, view.Sources[0].Previous)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Defaulted container app out of app, istio-proxy, setup. Previous instance.", text)

	text, _ = viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true,"containers":["app","istio-proxy"]}]}`)
	assert.Equal(t, "Viewing pods/webapp-7f9c in prod at the newest line. Containers app, istio-proxy. Previous instance.", text)

	text = refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"pods/webapp-7f9c","previous":true,"containers":["istio-proxy"]}]}`)
	assert.JSONEq(t, `{"error":"no-previous","source":0}`, text)

	f := watching()
	text, _ = viewOf(t, f, `{"sources":[{"namespace":"prod","resource":"deployments/webapp","previous":true}]}`)
	assert.Equal(t, "Viewing deployments/webapp in prod at the newest line. Defaulted container app out of app, istio-proxy, setup. Previous instance of 2 of 3 pods.", text)
	assert.Contains(t, f.queried[1], "c.name IN ('app')")
}

func TestAGrepThatDoesNotCompileIsRefused(t *testing.T) {
	text := refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"grep":"("}`)
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
		_, view := viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"anchor":`+anchor+`}`)
		assert.Equal(t, want, view.Anchor, anchor)
	}
	_, view := viewOf(t, watching(), webapp)
	assert.Equal(t, tools.LogsViewAnchor{Kind: tools.LogsAnchorTail}, view.Anchor, "absent is tail")
}

func TestAnUnreadableAnchorIsRefused(t *testing.T) {
	for _, anchor := range []string{`"yesterday"`, `"-2m"`, `"0s"`, `3`} {
		text := refusalOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"anchor":`+anchor+`}`)
		assert.Contains(t, text, `"field":"anchor"`, anchor)
		assert.NotContains(t, text, "yesterday")
	}
}

func TestPinToEndAbsentIsFalse(t *testing.T) {
	_, view := viewOf(t, watching(), webapp)
	assert.False(t, view.PinToEnd)
	_, view = viewOf(t, watching(), `{"sources":[{"namespace":"prod","resource":"deployments/webapp"}],"pin_to_end":true}`)
	assert.True(t, view.PinToEnd)
}

// The receipt with everything set: every source and its note, the anchor, the
// pin, and the grep.
func TestTheReceiptSpellsTheWholeView(t *testing.T) {
	text, view := viewOf(t, watching(), `{"description":"Why it crashed","sources":[
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

func TestTheActionCarriesTheDescription(t *testing.T) {
	_, _, shown := run(t, watching(), `{"description":"Tail the app","sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`)
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
		`[]`: `{"error":"bad-input"}`,
	} {
		text := refusalOf(t, watching(), raw)
		assert.JSONEq(t, want, text, raw)
		assert.NotContains(t, text, "hunter2", raw)
	}
}

func TestNoCacheIsARefusal(t *testing.T) {
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, &fakeService{readErr: cluster.ErrNotFound}, webapp))

	none := watching()
	none.active.Cache = nil
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, none, webapp))

	gone := watching()
	gone.answer = nil
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, gone, webapp))
}

func TestASyncingCacheIsARefusal(t *testing.T) {
	f := watching()
	f.health.Reason = "Connecting"
	assert.JSONEq(t, `{"error":"syncing"}`, refusalOf(t, f, webapp))
}

func TestAReadFailureCarriesNoText(t *testing.T) {
	f := watching()
	f.queryErr = errors.New("open cache 7: disk I/O error at /home/user")
	text := refusalOf(t, f, webapp)
	assert.JSONEq(t, `{"error":"read-failed"}`, text)
}

// Run is RunShown with the action dropped.
func TestRunAnswersTheReceipt(t *testing.T) {
	text, isError := New(watching(), func() time.Time { return clock }).Run(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(webapp))
	assert.False(t, isError)
	assert.True(t, strings.HasPrefix(text, "Viewing deployments/webapp in prod"), text)
}
