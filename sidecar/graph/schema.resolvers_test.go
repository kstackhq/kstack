package graph_test

// Behavioral tests for the GraphQL resolvers, exercised over a real gqlgen HTTP
// server. Fixtures and the fakeClusterService live in cluster_testutils_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amorey/beehive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/apimeta"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/catalog"
	"github.com/kstackhq/kstack/sidecar/internal/chatsvc"
	"github.com/kstackhq/kstack/sidecar/internal/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/memorysvc"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/securityconfig"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	agenttool "github.com/kstackhq/kstack/sidecar/internal/tools/agent"
	"github.com/kstackhq/kstack/sidecar/internal/tools/anthropicwebsearch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
	"github.com/kstackhq/kstack/sidecar/internal/tools/edit"
	"github.com/kstackhq/kstack/sidecar/internal/tools/kubequery"
	"github.com/kstackhq/kstack/sidecar/internal/tools/memory"
	"github.com/kstackhq/kstack/sidecar/internal/tools/read"
	"github.com/kstackhq/kstack/sidecar/internal/tools/taskstop"
	"github.com/kstackhq/kstack/sidecar/internal/tools/webfetch"
	"github.com/kstackhq/kstack/sidecar/internal/tools/write"

	"github.com/kstackhq/kstack/sidecar/graph"
	"github.com/kstackhq/kstack/sidecar/internal/auth"
	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/rawjson"
	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// --- Cluster ---

// The clusters query maps the beehive records including the nil → null
// convention for unset/never-probed pointer fields.
func TestClustersQuery(t *testing.T) {
	data := clustersQueryData(t, `{ clusters {
		id
		updatedAt
		spec { name syncEnabled enabled monitoringEnabled source { kubeconfig { context } } }
		status {
			source { kubeconfig { cluster { name entry { server insecureSkipTLSVerify } } user { name } isPresent isDefault } }
			server { uid version endpoint }
			principal { username groups }
		}
	} }`)

	clusters, ok := data["clusters"].([]any)
	if !ok || len(clusters) != 2 {
		t.Fatalf("want 2 clusters, got: %v", data["clusters"])
	}

	probed := clusters[0].(map[string]any)
	spec := probed["spec"].(map[string]any)
	if spec["name"] != "Production" || spec["syncEnabled"] != true || spec["enabled"] != true {
		t.Errorf("probed cluster spec: %v", spec)
	}
	if kcSrc := spec["source"].(map[string]any)["kubeconfig"].(map[string]any); kcSrc["context"] != "prod" {
		t.Errorf("probed cluster source: %v", kcSrc)
	}
	status := probed["status"].(map[string]any)
	if ci := status["server"].(map[string]any); ci["uid"] != "uid-1" || ci["version"] != "v1.29.3" {
		t.Errorf("probed server: %v", ci)
	}
	if p := status["principal"].(map[string]any); p["username"] != "system:admin" {
		t.Errorf("probed principal: %v", p)
	}
	kc := status["source"].(map[string]any)["kubeconfig"].(map[string]any)
	if kc["isPresent"] != true || kc["isDefault"] != true {
		t.Errorf("probed kubeconfig: %v", kc)
	}
	if c := kc["cluster"].(map[string]any); c["name"] != "prod-cluster" {
		t.Errorf("probed kubeconfig cluster: %v", c)
	}

	unprobed := clusters[1].(map[string]any)
	if name := unprobed["spec"].(map[string]any)["name"]; name != nil {
		t.Errorf("unset name should be null, got: %v", name)
	}
	unprobedStatus := unprobed["status"].(map[string]any)
	if ci := unprobedStatus["server"].(map[string]any); ci["uid"] != nil || ci["version"] != nil {
		t.Errorf("never-probed server should be null, got: %v", ci)
	}
	if p := unprobedStatus["principal"].(map[string]any); p["username"] != nil {
		t.Errorf("never-probed username should be null, got: %v", p)
	}
	if ep := unprobedStatus["server"].(map[string]any)["endpoint"]; ep != nil {
		t.Errorf("never-probed endpoint should be null, got: %v", ep)
	}
}

// Cluster.events maps the service's Events onto the wire: the run id rides
// the ObjectID scalar (decimal string), the type binds to the EventType enum
// (Normal/Warning), and the value slice is adapted to gqlgen's pointer slice.
func TestClusterEventsResolver(t *testing.T) {
	fix := clusterFixtures()
	svc := newFakeClusterService(fix)
	id := fix[0].id
	now := time.Now().UTC()
	svc.events[id] = []clustersvc.Event{{
		ID: 1, Category: "connection", Type: beehive.EventWarning,
		Reason: "ProbeFailed", Message: "boom", Count: 3, FirstAt: now, LastAt: now,
	}}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	query := `{ cluster(id: "` + string(id) + `") {
		events(category: "connection") { id category type reason message count firstAt lastAt }
	} }`
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))

	var resp struct {
		Data struct {
			Cluster struct {
				Events []map[string]any `json:"events"`
			} `json:"cluster"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	if len(resp.Data.Cluster.Events) != 1 {
		t.Fatalf("want 1 event, got %d: %s", len(resp.Data.Cluster.Events), raw)
	}
	ev := resp.Data.Cluster.Events[0]
	if ev["id"] != "1" {
		t.Errorf("id: want the run's decimal-string id, got %v", ev["id"])
	}
	if ev["type"] != "Warning" {
		t.Errorf("type: want Warning enum, got %v", ev["type"])
	}
	if ev["reason"] != "ProbeFailed" || ev["category"] != "connection" {
		t.Errorf("reason/category: %v", ev)
	}
	if ev["count"] != float64(3) {
		t.Errorf("count: want 3, got %v", ev["count"])
	}
}

// The cluster query returns the record for a tracked id.
func TestClusterQueryByID(t *testing.T) {
	data := clustersQueryData(t, `{ cluster(id: "2") {
		id
		spec { source { kubeconfig { context } } }
		status { source { kubeconfig { isPresent } } }
	} }`)

	cl, ok := data["cluster"].(map[string]any)
	if !ok || cl["id"] != "2" {
		t.Fatalf("want cluster 2, got: %v", data["cluster"])
	}
	spec := cl["spec"].(map[string]any)
	if kcSrc := spec["source"].(map[string]any)["kubeconfig"].(map[string]any); kcSrc["context"] != "staging" {
		t.Errorf("spec source: %v", spec)
	}
	if kc := cl["status"].(map[string]any)["source"].(map[string]any)["kubeconfig"].(map[string]any); kc["isPresent"] != false {
		t.Errorf("kubeconfig: %v", kc)
	}
}

// An untracked id resolves to null, not a GraphQL error.
func TestClusterQueryNotFound(t *testing.T) {
	data := clustersQueryData(t, `{ cluster(id: "999") { id } }`)
	if data["cluster"] != nil {
		t.Fatalf("want null cluster, got: %v", data["cluster"])
	}
}

// clustersWatch is a delta watch: the snapshot arrives as one Added change per
// cluster (not a single list frame), then the stream holds open (no completion)
// until the subscriber goes away.
func TestClustersWatchEmitsSnapshotAndStaysOpen(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	resp := openSSESubscription(t, srv.URL, "",
		"subscription { clustersWatch { type cluster { id spec { name } } } }")
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	// Collect frames until both fixtures have arrived; each is an Added change.
	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before snapshot completed")
			}
			if ev.event != "next" {
				continue
			}
			if !strings.Contains(ev.data, `"type":"Added"`) {
				t.Fatalf("snapshot change should be Added, got: %s", ev.data)
			}
			if strings.Contains(ev.data, `"id":"1"`) {
				seen["1"] = true
			}
			if strings.Contains(ev.data, `"id":"2"`) {
				seen["2"] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for snapshot; saw %v", seen)
		}
	}

	// The stream stays open after the snapshot (no completion).
	select {
	case _, ok := <-events:
		if !ok {
			t.Fatal("stream closed; want it held open")
		}
	case <-time.After(250 * time.Millisecond):
		// stayed open ✓
	}
}

// clusterEnabledSet writes through and returns the updated record; the change
// is visible in subsequent reads.
func TestClusterEnabledSetMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	raw := string(postGQL(t, srv.URL,
		`{"query":"mutation { clusterEnabledSet(id: \"1\", enabled: false) { id spec { enabled } } }"}`))
	if !strings.Contains(raw, `"enabled":false`) || strings.Contains(raw, `"errors"`) {
		t.Fatalf("mutation result: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"{ cluster(id: \"1\") { spec { enabled } } }"}`))
	if !strings.Contains(raw, `"enabled":false`) {
		t.Fatalf("change not visible to reads: %s", raw)
	}
}

// clusterSyncEnabledSet writes through the beehive store and returns the
// updated record; the change is visible in subsequent reads.
func TestClusterSyncEnabledSetMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	raw := string(postGQL(t, srv.URL,
		`{"query":"mutation { clusterSyncEnabledSet(id: \"1\", syncEnabled: false) { id spec { syncEnabled } } }"}`))
	if !strings.Contains(raw, `"syncEnabled":false`) || strings.Contains(raw, `"errors"`) {
		t.Fatalf("mutation result: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"{ cluster(id: \"1\") { spec { syncEnabled } } }"}`))
	if !strings.Contains(raw, `"syncEnabled":false`) {
		t.Fatalf("change not visible to reads: %s", raw)
	}
}

// clusterMonitoringEnabledSet is the third toggle, stored and served like the
// other two.
func TestClusterMonitoringEnabledSetMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	raw := string(postGQL(t, srv.URL,
		`{"query":"mutation { clusterMonitoringEnabledSet(id: \"1\", monitoringEnabled: true) { id spec { monitoringEnabled } } }"}`))
	if !strings.Contains(raw, `"monitoringEnabled":true`) || strings.Contains(raw, `"errors"`) {
		t.Fatalf("mutation result: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"{ cluster(id: \"1\") { spec { monitoringEnabled } } }"}`))
	if !strings.Contains(raw, `"monitoringEnabled":true`) {
		t.Fatalf("change not visible to reads: %s", raw)
	}
}

// clusterDelete marks the cluster for deletion; the record is no longer
// visible via the cluster query. An unknown id is a GraphQL error.
func TestClusterDeleteMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	raw := string(postGQL(t, srv.URL, `{"query":"mutation { clusterDelete(id: \"2\") }"}`))
	if !strings.Contains(raw, `"clusterDelete":true`) {
		t.Fatalf("delete result: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"{ cluster(id: \"2\") { id } }"}`))
	if !strings.Contains(raw, `"cluster":null`) {
		t.Fatalf("deleted cluster still readable: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"mutation { clusterDelete(id: \"999\") }"}`))
	if !strings.Contains(raw, `"errors"`) {
		t.Fatalf("want error for unknown id, got: %s", raw)
	}
}

// A delete the service refuses is a GraphQL error, and the record stays.
func TestClusterDeleteReportsARecordThatWouldNotDelete(t *testing.T) {
	cs := newFakeClusterService(clusterFixtures())
	cs.deleteErr = errors.New("store unavailable")
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: cs,
		Auth:       newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	_, errs := mutation(t, srv.URL, `mutation { clusterDelete(id: "2") }`)

	require.Len(t, errs, 1)
}

// clusterConnectionRetry answers true once the probe it asked for has run; what the
// probe found rides the cluster watch, not the reply. An unknown id is a GraphQL error.
func TestClusterConnectionRetryMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	raw := string(postGQL(t, srv.URL, `{"query":"mutation { clusterConnectionRetry(id: \"1\") }"}`))
	if !strings.Contains(raw, `"clusterConnectionRetry":true`) || strings.Contains(raw, `"errors"`) {
		t.Fatalf("retry result: %s", raw)
	}

	raw = string(postGQL(t, srv.URL, `{"query":"mutation { clusterConnectionRetry(id: \"999\") }"}`))
	if !strings.Contains(raw, `"errors"`) {
		t.Fatalf("want error for unknown id, got: %s", raw)
	}
}

// The status condition lists and the cache object resolve without panicking
// or erroring on bare fixtures: the cluster carries no conditions (empty arrays
// on the wire, never null) and the cache — streamed via clusterCachesWatch —
// has no on-disk files (exists=false, bytes=0, objectCount=0, kindCount=0).
func TestClusterEphemeralFields(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	// The cluster's own conditions are an empty list (never null).
	body, _ := json.Marshal(map[string]string{"query": `{ cluster(id: "1") { conditions { type status reason } } }`})
	raw := postGQL(t, srv.URL, string(body))
	var resp struct {
		Data struct {
			Cluster struct {
				Conditions []any `json:"conditions"`
			} `json:"cluster"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	if resp.Data.Cluster.Conditions == nil || len(resp.Data.Cluster.Conditions) != 0 {
		t.Errorf("conditions should be an empty list, got: %v", resp.Data.Cluster.Conditions)
	}

	// The cache resolves its conditions on a bare fixture. It has no status block: the
	// kind measures nothing itself.
	cache := firstCacheFrame(t, srv.URL)
	spec, _ := cache["spec"].(map[string]any)
	if spec["serverUid"] != "uid-1" || cache["clusterID"] != "1" {
		t.Errorf("cache identity: %v", cache)
	}
	if conds, ok := cache["conditions"].([]any); !ok || len(conds) != 0 {
		t.Errorf("sync conditions should be an empty list, got: %v", cache["conditions"])
	}
}

// Live conditions (cluster + cache) reach the wire with the correct GraphQL shapes —
// type/status/reason/message/liveness/timestamps. Conditions sit beside status, not
// inside it, since beehive stores them as their own object rows.
func TestConditionsAndSyncStatusOnWire(t *testing.T) {
	at := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	fixtures := clusterFixtures()
	fixtures[0].connConds = []clustersvc.Condition{{
		Type: string(clustersvc.ConditionConnected), Status: clustersvc.ConditionFalse,
		Reason: "ProbeFailed", Message: "connection refused",
		Liveness: true, TransitionedAt: at, UpdatedAt: at,
	}}
	fixtures[0].cacheConds = []clustersvc.Condition{{
		Type: string(clustersvc.ConditionSynced), Status: clustersvc.ConditionTrue,
		Reason: "Watching", Liveness: true, TransitionedAt: at, UpdatedAt: at,
	}}

	srv := newTestServer(t, fixtures)

	// The cluster's own conditions ride the cluster query.
	body, _ := json.Marshal(map[string]string{"query": `{ cluster(id: "1") {
		conditions { type status reason message liveness transitionedAt updatedAt }
	} }`})
	raw := postGQL(t, srv.URL, string(body))

	type wireCondition struct {
		Type           string  `json:"type"`
		Status         string  `json:"status"`
		Reason         string  `json:"reason"`
		Message        string  `json:"message"`
		Liveness       bool    `json:"liveness"`
		TransitionedAt *string `json:"transitionedAt"`
		UpdatedAt      *string `json:"updatedAt"`
	}
	var resp struct {
		Data struct {
			Cluster struct {
				Conditions []wireCondition `json:"conditions"`
			} `json:"cluster"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}

	conds := resp.Data.Cluster.Conditions
	if len(conds) != 1 {
		t.Fatalf("conditions: %+v", conds)
	}
	if conds[0].Type != "Connected" || conds[0].Status != "False" ||
		conds[0].Reason != "ProbeFailed" || conds[0].Message != "connection refused" ||
		!conds[0].Liveness || conds[0].TransitionedAt == nil || conds[0].UpdatedAt == nil {
		t.Errorf("Connected condition on the wire: %+v", conds[0])
	}

	// The cache's coarse Synced condition rides clusterCachesWatch. Freshness does not:
	// the sync children report it, out of band from the object graph.
	cache := firstCacheFrame(t, srv.URL)
	syncConds, _ := cache["conditions"].([]any)
	if len(syncConds) != 1 {
		t.Fatalf("Synced condition on the wire: %+v", cache["conditions"])
	}
	c0 := syncConds[0].(map[string]any)
	if c0["type"] != "Synced" || c0["status"] != "True" || c0["reason"] != "Watching" {
		t.Errorf("Synced condition on the wire: %+v", c0)
	}
}

// --- ClusterCache ---

// The plural root fields serve the same records as the nested ones, scoped by an
// OPTIONAL parent id: omit it for the whole fleet, pass it for one parent. The fixture
// gives each cluster one cache and each cache one sync record, so the two forms are
// distinguishable by count.
func TestPluralRootFieldsScopeOptionally(t *testing.T) {
	fix := clusterFixtures()
	srv := newTestServer(t, fix)
	clusterID := string(fix[0].id)
	cacheID := strconv.FormatInt(int64(fixtureCacheID(fix[0].id)), 10)

	tests := []struct {
		name      string
		query     string
		field     string
		wantCount int
	}{
		{"caches unscoped", `{ clusterCaches { id } }`, "clusterCaches", len(fix)},
		{"caches scoped", `{ clusterCaches(clusterID: "` + clusterID + `") { id } }`, "clusterCaches", 1},
		{"syncs unscoped", `{ clusterCachedKinds { id } }`, "clusterCachedKinds", len(fix)},
		{"syncs scoped", `{ clusterCachedKinds(cacheID: "` + cacheID + `") { id } }`, "clusterCachedKinds", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"query": tt.query})
			raw := postGQL(t, srv.URL, string(body))

			var resp struct {
				Data   map[string][]map[string]any
				Errors []struct{ Message string }
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode %s: %v", raw, err)
			}
			if len(resp.Errors) > 0 {
				t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
			}
			if got := len(resp.Data[tt.field]); got != tt.wantCount {
				t.Fatalf("want %d records, got %d: %s", tt.wantCount, got, raw)
			}
		})
	}
}

// ClusterCache.syncs completes the navigable path Cluster → caches → syncs.
func TestClusterCacheSyncsResolver(t *testing.T) {
	fix := clusterFixtures()
	srv := newTestServer(t, fix)
	cacheID := fixtureCacheID(fix[0].id)

	query := `{ clusterCache(id: "` + strconv.FormatInt(int64(cacheID), 10) + `") {
		cachedKinds { id owner { id kind } spec { apiVersion resource } }
	} }`
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))

	var resp struct {
		Data struct {
			ClusterCache struct {
				CachedKinds []map[string]any `json:"cachedKinds"`
			} `json:"clusterCache"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	got := resp.Data.ClusterCache.CachedKinds
	if len(got) != 1 {
		t.Fatalf("want this cache's one record, got %d: %s", len(got), raw)
	}
	if got[0]["id"] != strconv.FormatInt(int64(fixtureKindID(fix[0].id)), 10) {
		t.Errorf("id: want the sync record's own id, got %v", got[0]["id"])
	}
	if ownerOf(got[0])["id"] != strconv.FormatInt(int64(cacheID), 10) {
		t.Errorf("owner: want the cache's id, got %v", ownerOf(got[0]))
	}
}

// Cluster.caches is the navigable path down the owner chain — the only way into a
// cache by query without already holding its id. Asserted on the wire because a
// resolver that isn't wired returns an empty list rather than failing.
func TestClusterCachesResolver(t *testing.T) {
	fix := clusterFixtures()
	srv := newTestServer(t, fix)
	id := fix[0].id

	query := `{ cluster(id: "` + string(id) + `") {
		caches { id clusterID spec { serverUid } }
	} }`
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))

	var resp struct {
		Data struct {
			Cluster struct {
				Caches []map[string]any `json:"caches"`
			} `json:"cluster"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	got := resp.Data.Cluster.Caches
	if len(got) != 1 {
		t.Fatalf("want this cluster's one cache, got %d: %s", len(got), raw)
	}
	// Its own id, not its cluster's — the two differ in the fixture on purpose.
	if got[0]["id"] != strconv.FormatInt(int64(fixtureCacheID(id)), 10) {
		t.Errorf("id: want the cache's own id, got %v", got[0]["id"])
	}
	if got[0]["clusterID"] != string(id) {
		t.Errorf("clusterID: want the parent's id, got %v", got[0]["clusterID"])
	}
}

// The two cache-side event timelines are the same generic reader hung off a different
// record: `ClusterCache.events` reads the cache's own timeline (what the cache layer
// records, e.g. SyncStopped), `ClusterCachedKind.events` one synced kind's (where
// each worker report lands). One table because the wire mapping under test —
// clustersvc.Event → the generic Event shape, enum included — is identical; only the record it
// hangs off differs. Reaching either also exercises its root lookup, which is the only
// way into these records by query.
func TestCacheEventTimelineResolvers(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		// field is the root lookup; lookupID derives the record's own id from its
		// cluster's, since the two are deliberately different in the fixture.
		field    string
		lookupID func(clustersvc.ClusterID) clustersvc.ObjectID
		seed     func(*fakeClusterService, clustersvc.ClusterID, clustersvc.Event)
		event    clustersvc.Event
		wantEnum string
	}{{
		name:     "cache timeline",
		field:    "clusterCache",
		lookupID: func(id clustersvc.ClusterID) clustersvc.ObjectID { return clustersvc.ObjectID(fixtureCacheID(id)) },
		seed: func(f *fakeClusterService, id clustersvc.ClusterID, ev clustersvc.Event) {
			f.cacheEvents = map[clustersvc.ClusterCacheID][]clustersvc.Event{fixtureCacheID(id): {ev}}
		},
		event: clustersvc.Event{
			Category: "sync", Type: beehive.EventWarning, Reason: "SyncFailed",
			Message: "boom", Count: 2, FirstAt: now, LastAt: now,
		},
		wantEnum: "Warning",
	}, {
		name:     "per-kind sync timeline",
		field:    "clusterCachedKind",
		lookupID: func(id clustersvc.ClusterID) clustersvc.ObjectID { return clustersvc.ObjectID(fixtureKindID(id)) },
		seed: func(f *fakeClusterService, id clustersvc.ClusterID, ev clustersvc.Event) {
			f.syncEvents = map[clustersvc.ClusterCachedKindID][]clustersvc.Event{fixtureKindID(id): {ev}}
		},
		event: clustersvc.Event{
			Category: "sync", Type: beehive.EventNormal, Reason: "SyncComplete",
			Message: "cached 12 events", Count: 2, FirstAt: now, LastAt: now,
		},
		wantEnum: "Normal",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fix := clusterFixtures()
			svc := newFakeClusterService(fix)
			id := tt.lookupID(fix[0].id)
			ev := tt.event
			ev.ID = id
			tt.seed(svc, fix[0].id, ev)
			srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
				ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
			}))
			t.Cleanup(srv.Close)

			query := `{ ` + tt.field + `(id: "` + strconv.FormatInt(int64(id), 10) + `") {
				events(category: "sync") { id category type reason message count firstAt lastAt }
			} }`
			body, _ := json.Marshal(map[string]string{"query": query})
			raw := postGQL(t, srv.URL, string(body))

			var resp struct {
				Data map[string]struct {
					Events []map[string]any `json:"events"`
				}
				Errors []struct{ Message string }
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode %s: %v", raw, err)
			}
			if len(resp.Errors) > 0 {
				t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
			}
			got := resp.Data[tt.field].Events
			if len(got) != 1 {
				t.Fatalf("want 1 event, got %d: %s", len(got), raw)
			}
			if got[0]["type"] != tt.wantEnum {
				t.Errorf("type: want %s enum, got %v", tt.wantEnum, got[0]["type"])
			}
			if got[0]["reason"] != tt.event.Reason || got[0]["category"] != "sync" {
				t.Errorf("reason/category: %v", got[0])
			}
			if got[0]["count"] != float64(2) {
				t.Errorf("count: want 2, got %v", got[0]["count"])
			}
		})
	}
}

// clusterCacheClear empties one cache's file and returns the (still-tracked) record.
// The id it takes is the cache's own — a cluster id names no single cache once a UID
// migration has left two — so the fixture's cluster id must not resolve. An unknown id
// surfaces the not-found error.
func TestClusterCacheClearMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	body, _ := json.Marshal(map[string]string{"query": `mutation { clusterCacheClear(id: 101) { id } }`})
	raw := postGQL(t, srv.URL, string(body))
	if !strings.Contains(string(raw), `"id":"101"`) {
		t.Errorf("expected the cleared cache back, got %s", raw)
	}

	body, _ = json.Marshal(map[string]string{"query": `mutation { clusterCacheClear(id: 1) { id } }`})
	raw = postGQL(t, srv.URL, string(body))
	if !strings.Contains(string(raw), "errors") {
		t.Errorf("expected a GraphQL error for a cluster id, got %s", raw)
	}

	body, _ = json.Marshal(map[string]string{"query": `mutation { clusterCacheClear(id: "999") { id } }`})
	raw = postGQL(t, srv.URL, string(body))
	if !strings.Contains(string(raw), "errors") {
		t.Errorf("expected a GraphQL error for an unknown id, got %s", raw)
	}
}

// The wire keeps the positive form, matching the cluster's own two toggles, while the
// stored field is its inverse so a record written before the field decodes as syncing. One
// negation at the projection — so the round trip is what pins them together.
func TestClusterCachedKindSyncEnabledSetMutation(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())
	id := strconv.FormatInt(int64(fixtureKindID("1")), 10)

	body, _ := json.Marshal(map[string]string{
		"query": `mutation { clusterCachedKindSyncEnabledSet(id: ` + id + `, syncEnabled: false) { id spec { syncEnabled } } }`,
	})
	raw := postGQL(t, srv.URL, string(body))
	if !strings.Contains(string(raw), `"syncEnabled":false`) {
		t.Errorf("expected the paused record back, got %s", raw)
	}

	body, _ = json.Marshal(map[string]string{
		"query": `mutation { clusterCachedKindSyncEnabledSet(id: "999", syncEnabled: false) { id } }`,
	})
	raw = postGQL(t, srv.URL, string(body))
	if !strings.Contains(string(raw), "errors") {
		t.Errorf("expected a GraphQL error for an unknown id, got %s", raw)
	}
}

// TestClusterCachedKindsWatchIsCacheScoped pins the scoping on the wire: the stream is
// opened for one cache and must carry only that cache's kinds. The fixture gives each
// cache one record, so a leak shows up as a second frame.
func TestClusterCachedKindsWatchIsCacheScoped(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCachedKindsWatch(cacheID: "`+strconv.FormatInt(int64(fixtureCacheID("1")), 10)+`") { type kind { id owner { id kind } `+
			`spec { apiVersion kind resource namespaced } conditions { type status reason } } } }`)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	seen := 0
	deadline := time.After(time.Second)
	for {
		var frame struct {
			Data struct {
				Watch struct {
					Type string         `json:"type"`
					Kind map[string]any `json:"kind"`
				} `json:"clusterCachedKindsWatch"`
			} `json:"data"`
		}
		select {
		case ev, ok := <-events:
			if !ok {
				if seen == 0 {
					t.Fatal("stream closed before a frame arrived")
				}
				return
			}
			if ev.event != "next" {
				continue
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode cached-kind frame %s: %v", ev.data, err)
			}
			// Detect the snapshot boundary by type, never by a missing entity: an
			// errored non-null field nulls its parent, so a null entity also rides
			// ordinary frames.
			if frame.Data.Watch.Type == string(clustersvc.DeltaFrameBookmark) {
				continue
			}
			seen++
			cachedKind := frame.Data.Watch.Kind
			if ownerOf(cachedKind)["id"] != strconv.FormatInt(int64(fixtureCacheID("1")), 10) {
				t.Fatalf("another cache's record leaked into the stream: %v", cachedKind)
			}
			spec, _ := cachedKind["spec"].(map[string]any)
			if spec["resource"] != "deployments" {
				t.Errorf("resource = %v, want deployments", spec["resource"])
			}
		case <-deadline:
			if seen != 1 {
				t.Fatalf("expected exactly this cache's one record, saw %d", seen)
			}
			return
		}
	}
}

// TestDeltaWatchClosesSnapshotWithBookmark pins the boundary a consumer waits on before
// rendering an empty state: exactly one Bookmark, after the Added frames, carrying no
// entity. Without it a still-listing collection is indistinguishable from an empty one.
func TestDeltaWatchClosesSnapshotWithBookmark(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCachesWatch { type cache { id } } }`)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	added, bookmarks := 0, 0
	deadline := time.After(time.Second)
	for {
		var frame struct {
			Data struct {
				Watch struct {
					Type  string         `json:"type"`
					Cache map[string]any `json:"cache"`
				} `json:"clusterCachesWatch"`
			} `json:"data"`
		}
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before the bookmark arrived")
			}
			if ev.event != "next" {
				continue
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode cache frame %s: %v", ev.data, err)
			}
			switch frame.Data.Watch.Type {
			case string(clustersvc.DeltaFrameBookmark):
				bookmarks++
				if frame.Data.Watch.Cache != nil {
					t.Errorf("the bookmark carries no entity, got: %v", frame.Data.Watch.Cache)
				}
				if bookmarks == 1 && added != len(clusterFixtures()) {
					t.Errorf("bookmark closed the snapshot after %d of %d records", added, len(clusterFixtures()))
				}
			case string(clustersvc.DeltaFrameAdded):
				if bookmarks > 0 {
					t.Error("an Added frame arrived after the snapshot closed")
				}
				added++
			}
		case <-deadline:
			if bookmarks != 1 {
				t.Fatalf("expected exactly one bookmark, saw %d", bookmarks)
			}
			return
		}
	}
}

// TestClusterCacheStatsWatchServesGauge pins that the cache summary is reachable as a
// stream — the only shape that keeps reporting once the cache record itself settles.
func TestClusterCacheStatsWatchServesGauge(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCacheStatsWatch(id: "1", cacheID: "`+strconv.FormatInt(int64(fixtureCacheID("1")), 10)+`") { exists bytes objectCount kindCount } }`)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	deadline := time.After(2 * time.Second)
	for {
		var frame struct {
			Data struct {
				Stats map[string]any `json:"clusterCacheStatsWatch"`
			} `json:"data"`
		}
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before a frame arrived")
			}
			if ev.event != "next" {
				continue
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode stats frame %s: %v", ev.data, err)
			}
			if got := frame.Data.Stats["objectCount"]; got != float64(1386) {
				t.Errorf("objectCount = %v, want 1386", got)
			}
			if got := frame.Data.Stats["kindCount"]; got != float64(62) {
				t.Errorf("kindCount = %v, want 62", got)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for a stats frame")
		}
	}
}

// --- Cluster data ---
// The discovered kind catalog and the cached objects read out of a ClusterCache.

// ClusterCache.kinds maps the service's ClusterCachedDataKinds onto the wire 1:1
// (bound via gqlgen.yml), so the resolver just adapts the value slice to a pointer
// slice. Both ids it reads with come off the record, so the pair cannot disagree.
func TestClusterCachedDataKindsResolver(t *testing.T) {
	fix := clusterFixtures()
	svc := newFakeClusterService(fix)
	id := fix[0].id
	svc.kinds = map[clustersvc.ClusterID][]clustersvc.ClusterCachedDataKind{
		id: {
			{APIVersion: "apps/v1", Kind: "Deployment", Resource: "deployments", Scope: "Namespaced", IsCRD: false},
			{APIVersion: "example.com/v1", Kind: "Widget", Resource: "widgets", Scope: "Namespaced", IsCRD: true},
		},
	}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	query := `{ clusterCache(id: "` + strconv.FormatInt(int64(fixtureCacheID(id)), 10) + `") {
		kinds { apiVersion kind resource scope isCRD }
	} }`
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))

	var resp struct {
		Data struct {
			ClusterCache struct {
				Kinds []map[string]any `json:"kinds"`
			} `json:"clusterCache"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", resp.Errors)
	}
	if len(resp.Data.ClusterCache.Kinds) != 2 {
		t.Fatalf("want 2 kinds, got %d: %s", len(resp.Data.ClusterCache.Kinds), raw)
	}
	k := resp.Data.ClusterCache.Kinds[0]
	if k["apiVersion"] != "apps/v1" || k["kind"] != "Deployment" || k["resource"] != "deployments" {
		t.Errorf("first kind: %v", k)
	}
	if k["scope"] != "Namespaced" || k["isCRD"] != false {
		t.Errorf("first kind scope/isCRD: %v", k)
	}
	if resp.Data.ClusterCache.Kinds[1]["isCRD"] != true {
		t.Errorf("second kind should be a CRD: %v", resp.Data.ClusterCache.Kinds[1])
	}

	// An unknown id resolves the record to null rather than erroring, so the catalog
	// is never reached — where the root field used to answer with an empty list.
	q2 := `{ clusterCache(id: "99999") { kinds { kind } } }`
	b2, _ := json.Marshal(map[string]string{"query": q2})
	raw2 := postGQL(t, srv.URL, string(b2))
	var resp2 struct {
		Data struct {
			ClusterCache *struct {
				Kinds []map[string]any `json:"kinds"`
			} `json:"clusterCache"`
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw2, &resp2); err != nil {
		t.Fatalf("decode %s: %v", raw2, err)
	}
	if len(resp2.Errors) > 0 || resp2.Data.ClusterCache != nil {
		t.Fatalf("unknown cache should resolve to null, got %s", raw2)
	}
}

// clusterCachedDataObjectsWatch wires the subscription resolver to the service: the fake with
// no seeded objects opens an empty-until-ctx stream, so the SSE dial succeeds and the
// stream stays open with no frames rather than erroring.
func TestClusterCachedDataObjectsWatchOpensWithoutError(t *testing.T) {
	fix := clusterFixtures()
	svc := newFakeClusterService(fix)
	id := fix[0].id
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	idStr := string(id)
	q := `subscription { clusterCachedDataObjectsWatch(id: "` + idStr + `", cacheID: "` + idStr +
		`", apiVersion: "apps/v1", resource: "deployments") { type object { uid name } } }`
	resp := openSSESubscription(t, srv.URL, "", q)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	// The stub emits no frames; assert only that the dial produced no error frame within a
	// short window (a `next` carrying `errors`, or an SSE `error`/`complete` on open).
	select {
	case ev, ok := <-events:
		if ok && (ev.event == "error" || strings.Contains(ev.data, `"errors"`)) {
			t.Fatalf("objects watch should open cleanly, got %s: %s", ev.event, ev.data)
		}
	case <-time.After(200 * time.Millisecond):
		// No frame is the expected empty-until-ctx posture.
	}
}

// The resolver-gated `object` field carries the full native body as the JSON scalar,
// marshaled verbatim through gqlgen — a consumer selecting it gets the object JSON back
// as a nested value (not a string), with the identity fields alongside.
func TestClusterCachedDataObjectsWatchServesNativeBody(t *testing.T) {
	fix := clusterFixtures()
	svc := newFakeClusterService(fix)
	id := fix[0].id
	svc.dataObjects = map[clustersvc.ClusterID][]clustersvc.ClusterCachedDataObject{
		id: {{
			UID: "d1", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "web",
			RawJSON: rawjson.RawJSON(`{"kind":"Deployment","spec":{"replicas":3}}`),
		}},
	}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	idStr := string(id)
	q := `subscription { clusterCachedDataObjectsWatch(id: "` + idStr + `", cacheID: "` + idStr +
		`", apiVersion: "apps/v1", resource: "deployments") { type object { uid name rawJSON } } }`
	resp := openSSESubscription(t, srv.URL, "", q)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before the snapshot frame")
			}
			if ev.event != "next" {
				continue
			}
			var frame struct {
				Data struct {
					ClusterCachedDataObjectsWatch struct {
						Type   string `json:"type"`
						Object struct {
							UID     string         `json:"uid"`
							Name    string         `json:"name"`
							RawJSON map[string]any `json:"rawJSON"`
						} `json:"object"`
					} `json:"clusterCachedDataObjectsWatch"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode frame %s: %v", ev.data, err)
			}
			got := frame.Data.ClusterCachedDataObjectsWatch
			if got.Object.UID != "d1" || got.Object.Name != "web" {
				t.Fatalf("identity fields: got %+v", got.Object)
			}
			// The body decoded as a nested JSON object, not a string.
			if got.Object.RawJSON["kind"] != "Deployment" {
				t.Fatalf("native body not served as JSON: %s", ev.data)
			}
			if spec, _ := got.Object.RawJSON["spec"].(map[string]any); spec == nil || spec["replicas"] != float64(3) {
				t.Fatalf("nested body fields missing: %s", ev.data)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for the object snapshot frame")
		}
	}
}

// clusterCachedDataKindsWatch streams the kind catalog as a delta watch: the resolver
// adapts the service's ClusterCachedDataKindWatchFrame stream to the wire 1:1, so the snapshot
// arrives as Added changes carrying the kind's fields (incl. the live count) and the
// stream stays open for live updates.
func TestClusterCachedDataKindsWatchEmitsSnapshotAndStaysOpen(t *testing.T) {
	fix := clusterFixtures()
	svc := newFakeClusterService(fix)
	id := fix[0].id
	svc.kinds = map[clustersvc.ClusterID][]clustersvc.ClusterCachedDataKind{
		id: {
			{APIVersion: "apps/v1", Kind: "Deployment", Resource: "deployments", Scope: "Namespaced", IsCRD: false, Count: 3},
			{APIVersion: "example.com/v1", Kind: "Widget", Resource: "widgets", Scope: "Namespaced", IsCRD: true, Count: 0},
		},
	}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{}),
	}))
	t.Cleanup(srv.Close)

	q := `subscription { clusterCachedDataKindsWatch(id: "` + string(id) +
		`", cacheID: "` + string(id) + `") { type kind { apiVersion kind resource count } } }`
	resp := openSSESubscription(t, srv.URL, "", q)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	seen := map[string]int{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before snapshot completed")
			}
			if ev.event != "next" {
				continue
			}
			if !strings.Contains(ev.data, `"type":"Added"`) {
				t.Fatalf("snapshot change should be Added, got: %s", ev.data)
			}
			var frame struct {
				Data struct {
					ClusterCachedDataKindsWatch struct {
						Type string `json:"type"`
						Kind struct {
							Kind  string `json:"kind"`
							Count int    `json:"count"`
						} `json:"kind"`
					} `json:"clusterCachedDataKindsWatch"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode frame %s: %v", ev.data, err)
			}
			seen[frame.Data.ClusterCachedDataKindsWatch.Kind.Kind] = frame.Data.ClusterCachedDataKindsWatch.Kind.Count
		case <-deadline:
			t.Fatalf("timed out waiting for snapshot; saw %v", seen)
		}
	}
	if seen["Deployment"] != 3 {
		t.Errorf("Deployment count: want 3, got %d", seen["Deployment"])
	}
	if _, ok := seen["Widget"]; !ok {
		t.Errorf("Widget kind missing from snapshot: %v", seen)
	}

	// The stream stays open after the snapshot (no completion).
	select {
	case _, ok := <-events:
		if !ok {
			t.Fatal("stream closed; want it held open")
		}
	case <-time.After(250 * time.Millisecond):
		// stayed open ✓
	}
}

// --- Cloud account ---

// postGQL POSTs a GraphQL query/mutation body to url's /graphql endpoint and
// returns the raw response.
func postGQL(t *testing.T, url, body string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw
}

// The fakes (memCredStore/fakeOAuthFlow/fakeLoopback) live in testutils_test.go;
// the postGQL / SSE helpers are defined above + in server_test.go.

// The authState query reflects a signed-out auth service (no stale identity).
func TestAuthStateQuerySignedOut(t *testing.T) {
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: newFakeAuth(auth.Identity{})}))
	defer srv.Close()

	raw := string(postGQL(t, srv.URL, `{"query":"{ authState { authenticated identity { sub } } }"}`))
	if !strings.Contains(raw, `"identity":null`) {
		t.Fatalf("want signed-out auth state (null identity), got: %s", raw)
	}
	if !strings.Contains(raw, `"authenticated":false`) {
		t.Fatalf("want authenticated false, got: %s", raw)
	}
}

// signedInAuth returns a fake auth.Service already signed in as the given
// identity. The resolver depends on the auth.Service interface, so the tests fake
// it (see fakeAuth) rather than constructing the real service.
func signedInAuth(t *testing.T, id auth.Identity) auth.Service {
	t.Helper()
	return signedInFakeAuth(id)
}

// configuredAuth returns a signed-out fake auth.Service that signs in as id when
// Login runs (the resolver's login flow).
func configuredAuth(t *testing.T, id auth.Identity) auth.Service {
	t.Helper()
	return newFakeAuth(id)
}

// The authState query reflects the current signed-in identity.
func TestAuthStateQuerySignedIn(t *testing.T) {
	svc := signedInAuth(t, auth.Identity{UserID: "u1", Email: "a@x.com", Name: "Ada"})

	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: svc}))
	defer srv.Close()

	raw := string(postGQL(t, srv.URL, `{"query":"{ authState { authenticated identity { sub email name } } }"}`))
	if !strings.Contains(raw, `"email":"a@x.com"`) {
		t.Fatalf("want signed-in identity, got: %s", raw)
	}
	if !strings.Contains(raw, `"authenticated":true`) {
		t.Fatalf("want authenticated true, got: %s", raw)
	}
}

// authStateWatch emits the current snapshot first, then a fresh snapshot on change.
func TestAuthStateWatchSnapshotThenDelta(t *testing.T) {
	svc := configuredAuth(t, auth.Identity{Email: "a@x.com"})
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: svc}))
	defer srv.Close()

	resp := openSSESubscription(t, srv.URL, "",
		"subscription { authStateWatch { authenticated identity { email } } }")
	defer resp.Body.Close() // ends the subscription; must run before srv.Close()
	events := sseEvents(t, resp)

	if ev := nextSSE(t, events); ev.event != "next" || !strings.Contains(ev.data, `"authenticated":false`) {
		t.Fatalf("first frame: event=%q data=%s", ev.event, ev.data)
	}

	if err := svc.StartLogin(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if ev := nextSSE(t, events); ev.event != "next" || !strings.Contains(ev.data, `"email":"a@x.com"`) {
		t.Fatalf("delta frame: event=%q data=%s", ev.event, ev.data)
	}
}

// logout delegates to the account: returns true and flips the session signed-out.
func TestLogoutMutation(t *testing.T) {
	svc := signedInFakeAuth(auth.Identity{})
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: svc}))
	defer srv.Close()

	raw := string(postGQL(t, srv.URL, `{"query":"mutation { authLogout }"}`))
	if !strings.Contains(raw, `"authLogout":true`) {
		t.Fatalf("want authLogout true, got: %s", raw)
	}
	if cur, _ := svc.Current(context.Background()); cur.Authenticated {
		t.Fatal("session still signed in after logout")
	}
}

// login is non-blocking: it returns true immediately and the resulting signed-in
// session arrives asynchronously (observed here via the auth-state watch), proving
// the mutation kicked off the flow without blocking on the browser round-trip.
func TestLoginMutationKicksOffFlow(t *testing.T) {
	svc := newFakeAuth(auth.Identity{Email: "a@x.com"})

	// The auth-state stream is latest-value (current-on-subscribe): the first State
	// is the signed-out baseline, and the signed-in flow surfaces as a later State
	// with Authenticated true. The loop skips the baseline and waits for sign-in.
	states, cancel := svc.Subscribe()
	defer cancel()

	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: svc}))
	defer srv.Close()

	raw := string(postGQL(t, srv.URL, `{"query":"mutation { authLoginStart }"}`))
	if !strings.Contains(raw, `"authLoginStart":true`) {
		t.Fatalf("want login true, got: %s", raw)
	}

	for {
		select {
		case st := <-states:
			if st.Authenticated {
				if st.Identity == nil || st.Identity.Email != "a@x.com" {
					t.Fatalf("signed-in identity = %+v", st.Identity)
				}
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("login never produced a signed-in session")
		}
	}
}

// A synchronous setup failure (loopback bind / browser launch) surfaces as a
// GraphQL error rather than a silent login:true — the whole point of running the
// flow's setup phase synchronously.
func TestLoginMutationSurfacesSetupError(t *testing.T) {
	svc := newFakeAuth(auth.Identity{Email: "a@x.com"})
	svc.loginErr = errors.New("loopback bind failed")

	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{Auth: svc}))
	defer srv.Close()

	raw := string(postGQL(t, srv.URL, `{"query":"mutation { authLoginStart }"}`))
	if !strings.Contains(raw, `"errors"`) {
		t.Fatalf("want GraphQL error for a setup failure, got: %s", raw)
	}
	if strings.Contains(raw, `"authLoginStart":true`) {
		t.Fatalf("login must not report true when setup failed, got: %s", raw)
	}
}

// TestClusterCacheSyncStatusWatchServesEveryKind pins the one field on the wire that carries
// a per-kind verdict: nothing else can say which of a cache's hundred kinds is failing.
func TestClusterCacheSyncStatusWatchServesEveryKind(t *testing.T) {
	srv := newTestServer(t, clusterFixtures())

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCacheSyncStatusWatch(id: "1", cacheID: "`+strconv.FormatInt(int64(fixtureCacheID("1")), 10)+`") { cacheID discovery { reason } kinds { apiVersion resource reason restarts objectCount } } }`)
	defer resp.Body.Close()
	events := sseEvents(t, resp)

	deadline := time.After(2 * time.Second)
	for {
		var frame struct {
			Data struct {
				Status struct {
					CacheID   string           `json:"cacheID"`
					Discovery map[string]any   `json:"discovery"`
					Kinds     []map[string]any `json:"kinds"`
				} `json:"clusterCacheSyncStatusWatch"`
			} `json:"data"`
		}
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before a frame arrived")
			}
			if ev.event != "next" {
				continue
			}
			if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
				t.Fatalf("decode sync status frame %s: %v", ev.data, err)
			}
			if got := frame.Data.Status.Discovery["reason"]; got != "Discovered" {
				t.Errorf("discovery reason = %v, want Discovered", got)
			}
			if len(frame.Data.Status.Kinds) == 0 {
				t.Fatal("a cache with mirrored kinds served none")
			}
			for _, kind := range frame.Data.Status.Kinds {
				if kind["apiVersion"] == "" || kind["resource"] == "" {
					t.Errorf("a kind row identifies nothing: %v", kind)
				}
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for a sync status frame")
		}
	}
}

// The descriptors reach the wire off the kinds watch, typed — not as the JSON the store holds.
func TestPrinterColumnsReachTheWire(t *testing.T) {
	svc := newFakeClusterService(clusterFixtures())
	svc.kinds = map[clustersvc.ClusterID][]clustersvc.ClusterCachedDataKind{
		"1": {{
			APIVersion: "example.com/v1", Kind: "Widget", Resource: "widgets", Scope: "Namespaced", IsCRD: true,
			PrinterColumns: []clustersvc.PrinterColumn{
				{Name: "Replicas", Type: "integer", JSONPath: ".spec.replicas", Priority: 1},
			},
		}},
	}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{})}))
	t.Cleanup(srv.Close)

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCachedDataKindsWatch(id: "1", cacheID: "1") { kind { printerColumns { name type jsonPath priority } } } }`)
	defer resp.Body.Close()

	frame := testutil.Recv(t, sseEvents(t, resp), "the first frame")

	assert.Contains(t, frame.data, `"name":"Replicas"`)
	assert.Contains(t, frame.data, `"jsonPath":".spec.replicas"`)
	assert.Contains(t, frame.data, `"priority":1`)
}

// A timestamp the source object never carried serializes as null, not as 0001-01-01. The records
// keep a value time.Time — the delta-watch diff compares frames with ==, so they must stay
// comparable — and this is the wire's whole answer for "absent".
func TestAnAbsentTimestampSerializesAsNull(t *testing.T) {
	seen := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	svc := newFakeClusterService(clusterFixtures())
	svc.dataEvents = map[clustersvc.ClusterID][]clustersvc.ClusterCachedDataEvent{
		"1": {{UID: "e-1", LastSeen: seen}},
	}
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{ClusterSvc: svc, Auth: newFakeAuth(auth.Identity{})}))
	t.Cleanup(srv.Close)

	resp := openSSESubscription(t, srv.URL, "",
		`subscription { clusterCachedDataEventsWatch(id: "1", cacheID: "1") { event { firstSeen lastSeen } } }`)
	defer resp.Body.Close()

	frame := testutil.Recv(t, sseEvents(t, resp), "the first frame")

	assert.Contains(t, frame.data, `"firstSeen":null`)
	assert.Contains(t, frame.data, `"lastSeen":"2026-08-30T12:00:00Z"`)
}

// --- Chat ---

// The cluster service's named errors reach the client as codes the same way: an id
// naming nothing is not found, and a record its source still declares or one that
// will not be connected is a conflict with the record's own state.
func TestClusterRefusalsCarryTheirCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{clustersvc.ErrNotFound, "KSTACK_RECORD_NOT_FOUND"},
		{clustersvc.ErrDeclaredBySource, "KSTACK_CONFLICT"},
		{clustersvc.ErrNotConnectable, "KSTACK_CONFLICT"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
				ClusterSvc: errClusterService{err: tc.err}, Auth: newFakeAuth(auth.Identity{}),
			}))
			t.Cleanup(srv.Close)

			for _, q := range []string{
				`mutation { clusterEnabledSet(id: "1", enabled: true) { id } }`,
				`mutation { clusterSyncEnabledSet(id: "1", syncEnabled: true) { id } }`,
				`mutation { clusterConnectionRetry(id: "1") }`,
				`mutation { clusterDelete(id: "1") }`,
			} {
				_, errs := mutation(t, srv.URL, q)
				require.Len(t, errs, 1, q)
				assert.Equal(t, tc.code, errs[0].Extensions["code"], q)
			}
		})
	}
}

// --- Error arms ---

// Every resolver that can fail hands the service's error straight back, and none
// of them swallows one into a zero value. Driven through the resolver interfaces
// rather than the wire: the arm under test is the same one either way, and one
// table keeps a newly added resolver visibly absent.
func TestResolverErrorsReachTheCaller(t *testing.T) {
	wantErr := errors.New("service unavailable")
	r := &graph.Resolver{
		ClusterSvc: errClusterService{err: wantErr},
		Auth:       errAuth{err: wantErr},
	}
	ctx := context.Background()
	cluster := &clustersvc.Cluster{ID: "1"}
	cache := &clustersvc.ClusterCache{
		RecordMeta: clustersvc.RecordMeta{ID: 101},
		ClusterID:  "1",
	}
	kind := &clustersvc.ClusterCachedKind{RecordMeta: clustersvc.RecordMeta{ID: 301}}
	id := clustersvc.ClusterID("1")
	objID := clustersvc.ObjectID(1)

	// Each case calls one resolver and reports only its error; the values are
	// asserted by the tests above, on a service that works.
	calls := map[string]func() error{
		"Cluster.caches":                     func() error { _, err := r.Cluster().Caches(ctx, cluster); return err },
		"Cluster.events":                     func() error { _, err := r.Cluster().Events(ctx, cluster, nil, nil); return err },
		"ClusterCache.kinds":                 func() error { _, err := r.ClusterCache().Kinds(ctx, cache); return err },
		"ClusterCache.cachedKinds":           func() error { _, err := r.ClusterCache().CachedKinds(ctx, cache); return err },
		"ClusterCache.events":                func() error { _, err := r.ClusterCache().Events(ctx, cache, nil, nil); return err },
		"ClusterCachedKind.events":           func() error { _, err := r.ClusterCachedKind().Events(ctx, kind, nil, nil); return err },
		"Query.cluster":                      func() error { _, err := r.Query().Cluster(ctx, id); return err },
		"Query.clusters":                     func() error { _, err := r.Query().Clusters(ctx); return err },
		"Query.clusterCache":                 func() error { _, err := r.Query().ClusterCache(ctx, objID); return err },
		"Query.clusterCaches":                func() error { _, err := r.Query().ClusterCaches(ctx, nil); return err },
		"Query.clusterCaches(scoped)":        func() error { _, err := r.Query().ClusterCaches(ctx, &id); return err },
		"Query.clusterCachedKind":            func() error { _, err := r.Query().ClusterCachedKind(ctx, objID); return err },
		"Query.clusterCachedKinds":           func() error { _, err := r.Query().ClusterCachedKinds(ctx, nil); return err },
		"Query.clusterCachedKinds(scoped)":   func() error { _, err := r.Query().ClusterCachedKinds(ctx, &objID); return err },
		"Query.authState":                    func() error { _, err := r.Query().AuthState(ctx); return err },
		"Mutation.clusterEnabledSet":         func() error { _, err := r.Mutation().ClusterEnabledSet(ctx, id, true); return err },
		"Mutation.clusterSyncEnabledSet":     func() error { _, err := r.Mutation().ClusterSyncEnabledSet(ctx, id, true); return err },
		"Mutation.clusterMonitoringSet":      func() error { _, err := r.Mutation().ClusterMonitoringEnabledSet(ctx, id, true); return err },
		"Mutation.clusterConnectionRetry":    func() error { _, err := r.Mutation().ClusterConnectionRetry(ctx, id); return err },
		"Mutation.clusterDelete":             func() error { _, err := r.Mutation().ClusterDelete(ctx, id); return err },
		"Mutation.clusterCacheClear":         func() error { _, err := r.Mutation().ClusterCacheClear(ctx, objID); return err },
		"Mutation.clusterCachedKindSyncSet":  func() error { _, err := r.Mutation().ClusterCachedKindSyncEnabledSet(ctx, objID, true); return err },
		"Mutation.authLogout":                func() error { _, err := r.Mutation().AuthLogout(ctx); return err },
		"Subscription.eventsWatch":           func() error { _, err := r.Subscription().EventsWatch(ctx, objID, nil); return err },
		"Subscription.clustersWatch":         func() error { _, err := r.Subscription().ClustersWatch(ctx); return err },
		"Subscription.clusterEventsWatch":    func() error { _, err := r.Subscription().ClusterEventsWatch(ctx, id, nil); return err },
		"Subscription.clusterScheduleWatch":  func() error { _, err := r.Subscription().ClusterScheduleWatch(ctx, id); return err },
		"Subscription.clusterCachesWatch":    func() error { _, err := r.Subscription().ClusterCachesWatch(ctx); return err },
		"Subscription.cacheHealthWatch":      func() error { _, err := r.Subscription().ClusterCacheHealthWatch(ctx); return err },
		"Subscription.cachedKindsWatch":      func() error { _, err := r.Subscription().ClusterCachedKindsWatch(ctx, objID); return err },
		"Subscription.cacheStatsWatch":       func() error { _, err := r.Subscription().ClusterCacheStatsWatch(ctx, id, objID); return err },
		"Subscription.cacheSyncStatusWatch":  func() error { _, err := r.Subscription().ClusterCacheSyncStatusWatch(ctx, id, objID); return err },
		"Subscription.cachedDataKindsWatch":  func() error { _, err := r.Subscription().ClusterCachedDataKindsWatch(ctx, id, objID); return err },
		"Subscription.cachedDataEventsWatch": func() error { _, err := r.Subscription().ClusterCachedDataEventsWatch(ctx, id, objID); return err },

		"Subscription.cachedDataObjectsWatch": func() error {
			_, err := r.Subscription().ClusterCachedDataObjectsWatch(ctx, id, objID, "apps/v1", "deployments")
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, wantErr) {
				t.Errorf("err = %v, want %v", err, wantErr)
			}
		})
	}
}

// authLoginStart's setup failure is the mutation's own arm, and the only one
// fakeAuth models separately from the login flow it kicks off.
func TestAuthLoginStartSurfacesSetupErrorToTheResolver(t *testing.T) {
	wantErr := errors.New("loopback bind failed")
	fake := newFakeAuth(auth.Identity{})
	fake.loginErr = wantErr
	r := &graph.Resolver{Auth: fake}

	ok, err := r.Mutation().AuthLoginStart(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if ok {
		t.Error("authLoginStart = true on a setup failure, want false")
	}
}

// ClusterPrincipal.permissions is the one field with no implementation behind
// it: selecting it errors rather than resolving to an empty grant, so a
// consumer can't read "no permissions" out of "not built yet".
func TestClusterPrincipalPermissionsIsNotImplemented(t *testing.T) {
	r := &graph.Resolver{}
	perms, err := r.ClusterPrincipal().Permissions(context.Background(), &clustersvc.ClusterPrincipal{}, "default")
	if err == nil {
		t.Fatalf("permissions resolved to %+v, want an error", perms)
	}
}

// The three watches with no wire test of their own open against a working
// service and carry their first value: eventsWatch's snapshot boundary, the
// schedule gauge's current value, and one cache's health verdict. Each also
// exercises the stream adapter behind it — watchStream for the frame types,
// ptrStream for the bare gauge.
func TestGaugeAndTimelineSubscriptionsCarryTheirFirstValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &graph.Resolver{ClusterSvc: newFakeClusterService(clusterFixtures())}

	events, err := r.Subscription().EventsWatch(ctx, 1, nil)
	if err != nil {
		t.Fatalf("eventsWatch: %v", err)
	}
	// The fixture logs no events, so the Bookmark alone closes the snapshot.
	if frame := testutil.Recv(t, events, "eventsWatch frame"); frame.Type != clustersvc.EventFrameBookmark {
		t.Errorf("first frame = %v, want Bookmark", frame.Type)
	}
	clusterEvents, err := r.Subscription().ClusterEventsWatch(ctx, "1", nil)
	if err != nil {
		t.Fatalf("clusterEventsWatch: %v", err)
	}
	if frame := testutil.Recv(t, clusterEvents, "clusterEventsWatch frame"); frame.Type != clustersvc.EventFrameBookmark {
		t.Errorf("first frame = %v, want Bookmark", frame.Type)
	}

	schedule, err := r.Subscription().ClusterScheduleWatch(ctx, "1")
	if err != nil {
		t.Fatalf("clusterScheduleWatch: %v", err)
	}
	if s := testutil.Recv(t, schedule, "schedule gauge"); !s.Probing {
		t.Errorf("schedule = %+v, want the probing value the fake publishes", s)
	}

	health, err := r.Subscription().ClusterCacheHealthWatch(ctx)
	if err != nil {
		t.Fatalf("clusterCacheHealthWatch: %v", err)
	}
	if v := testutil.Recv(t, health, "cache health verdict"); v.CacheID == 0 {
		t.Errorf("verdict = %+v, want one keyed to a cache", v)
	}
}

// Every Dialect member is named for its API, and the Go set and the schema's are
// the same set: gqlgen refuses a member with no constant behind it at generate
// time, and this is the other direction, a constant the schema does not declare.
func TestLLMDialectBindsEveryMember(t *testing.T) {
	declared := map[string]bool{}
	for _, v := range graph.NewExecutableSchema(graph.Config{}).Schema().Types["LLMDialect"].EnumValues {
		declared[strings.ToLower(v.Name)] = true
	}
	held := map[string]bool{}
	for _, p := range llm.Dialects {
		held[string(p)] = true
	}
	assert.Equal(t, held, declared)
}

// --- chat ---

// newChatServer is a GraphQL server over a real chat service on the fake,
// with cluster "1" seeded for sends to file under.
func newChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _ := newChatServerOver(t)
	return srv
}

// newSandboxedChatServer is newChatServer on a machine whose sandbox is status.
func newSandboxedChatServer(t *testing.T, status sandbox.Status) *httptest.Server {
	t.Helper()
	srv, _, _ := newChatServerWith(t, status)
	return srv
}

// newChatServerOver is newChatServer handing back the app.db, for a test that fails
// the store by closing it, and the fake, for one that stages a reply.
func newChatServerOver(t *testing.T) (*httptest.Server, *appdb.DB, *llm.Fake) {
	t.Helper()
	return newChatServerWith(t, sandbox.Status{})
}

// newChatServerWith is newChatServerOver on a machine whose sandbox is status.
func newChatServerWith(t *testing.T, status sandbox.Status) (*httptest.Server, *appdb.DB, *llm.Fake) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Write.Exec(`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES ('1', 'kubeconfig', 'ctx', 0, 0)`)
	require.NoError(t, err)
	fake := llm.NewFake(0)
	cat := catalog.New(catalog.Config{Fake: fake})
	llmSvc := llm.New(cat.Providers()...)
	// The search is offered, since a test stages a turn that searched; the rest
	// is read alone, so no call runs while stored calls still show.
	box := tools.NewBox([]tools.Tool{agenttool.New(), anthropicwebsearch.New(time.Now)}, bash.Reader{}, &read.Tool{}, &write.Tool{}, &edit.Tool{}, &webfetch.Tool{}, taskstop.New(), memory.New(nil), kubequery.New(nil))
	chatSvc, err := chatsvc.New(db, filepath.Join(t.TempDir(), "chats"), llmSvc, clustercard.New(newFakeClusterService(nil)), nil, box, cat, status, testSecurity(t))
	require.NoError(t, err)
	stop, err := chatSvc.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, stop(context.Background()))
		require.NoError(t, chatSvc.Close())
	})
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{
		ClusterSvc: newFakeClusterService(nil), ChatSvc: chatSvc, LLMSvc: llmSvc, Auth: newFakeAuth(auth.Identity{}),
		SandboxStatus: status,
	}))
	t.Cleanup(srv.Close)
	return srv, db, fake
}

// mutate posts one operation and returns its data, failing on any error in the response.
func mutate(t *testing.T, srv *httptest.Server, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))
	var resp struct {
		Data   map[string]any
		Errors []struct{ Message string }
	}
	require.NoError(t, json.Unmarshal(raw, &resp), "%s", raw)
	require.Empty(t, resp.Errors)
	return resp.Data
}

// chatSend returns the answer row: streaming, on the names the send gave, with the
// provider labelled by its id and no thinking.
func TestChatSendReturnsTheAnswerRow(t *testing.T) {
	srv := newChatServer(t)

	data := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "how many pods?") {
		seq role status model effort thinking provider { id label } content
	} }`)

	assert.Equal(t, map[string]any{
		"seq": float64(1), "role": "Assistant", "status": "Streaming", "model": "fake", "effort": "high",
		"thinking": "", "provider": map[string]any{"id": "fake", "label": "Fake"}, "content": []any{},
	}, data["chatSend"])
}

// A send naming a model on another dialect than the chat's last answer's is passed
// through like any other.
func TestChatSendAcceptsAModelOnAnotherDialect(t *testing.T) {
	srv, db, _ := newChatServerOver(t)
	for _, stmt := range []string{
		`INSERT INTO chats (id, cluster_id, mode, created_at, updated_at) VALUES ('c', '1', 'chat', 0, 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, created_at) VALUES ('u', 'c', 0, 'user', '[{"type":"text","text":"hi"}]', 0)`,
		`INSERT INTO agent_runs (id, agent_type, app_version, trigger, chat_id, trigger_message_id, provider, model, dialect, status, created_at)
		 VALUES ('r', 'chat', 'test', 'chat', 'c', 'u', 'anthropic', 'claude-haiku-4-5', 'messages', 'succeeded', 0)`,
		`INSERT INTO messages (id, chat_id, seq, role, content, run_id, created_at) VALUES ('a', 'c', 1, 'assistant', '[{"type":"text","text":"Hello."}]', 'r', 0)`,
	} {
		_, err := db.Write.Exec(stmt)
		require.NoError(t, err)
	}

	data := mutate(t, srv, `mutation { chatSend(chatID: "c", mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "and now?") { chatID provider { id } } }`)

	assert.Equal(t, map[string]any{"chatID": "c", "provider": map[string]any{"id": "fake"}}, data["chatSend"])
}

// chatFrames opens a subscription over the chat server and returns its events.
// The caller closes the response body, which is what cancels the subscription.
func chatFrames(t *testing.T, srv *httptest.Server, query string) (*http.Response, <-chan sseEvent) {
	t.Helper()
	resp := openSSESubscription(t, srv.URL, "", query)
	return resp, sseEvents(t, resp)
}

// A chat with no turn running is cancelled without error, and its rows stay: the
// mutation answers true whether or not there was anything to stop.
func TestChatCancelOfAnIdleChatAnswersTrue(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	data := mutate(t, srv, `mutation { chatCancel(chatID: "`+chatID+`") }`)

	assert.Equal(t, map[string]any{"chatCancel": true}, data)
}

// Delete answers true and the chat leaves the list; deleting one already gone is
// true too, since the row the caller asked to be rid of is gone either way.
func TestChatDeleteTakesTheChatOffTheList(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	assert.Equal(t, map[string]any{"chatDelete": true}, mutate(t, srv, `mutation { chatDelete(id: "`+chatID+`") }`))
	assert.Equal(t, map[string]any{"chatDelete": true}, mutate(t, srv, `mutation { chatDelete(id: "`+chatID+`") }`))

	resp, events := chatFrames(t, srv, `subscription { chatsWatch { type chat { id } } }`)
	defer resp.Body.Close()
	assert.Equal(t, "Bookmark", firstChatFrameType(t, events), "the snapshot is empty")
}

// A rename answers with the chat as committed, not as asked: the service trims the
// title and moves updatedAt.
func TestChatRenameServesTheCommittedChat(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	data := mutate(t, srv, `mutation { chatRename(id: "`+chatID+`", title: "  Renamed  ") { id title createdAt updatedAt } }`)

	chat := data["chatRename"].(map[string]any)
	assert.Equal(t, chatID, chat["id"])
	assert.Equal(t, "Renamed", chat["title"])
	createdAt, err := time.Parse(time.RFC3339Nano, chat["createdAt"].(string))
	require.NoError(t, err)
	updatedAt, err := time.Parse(time.RFC3339Nano, chat["updatedAt"].(string))
	require.NoError(t, err)
	assert.False(t, updatedAt.Before(createdAt), "updatedAt %s is before createdAt %s", updatedAt, createdAt)
}

// A send with no chat creates one, filed under the mode and cluster it names. The
// other sends all say Chat, so this is the one that carries Dashboard through.
func TestChatSendWithNoChatCreatesOne(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Dashboard, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	resp, events := chatFrames(t, srv, `subscription { chatsWatch { type chat { id mode clusterID } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool { return f["type"] == "Added" })
	assert.Equal(t, map[string]any{"id": chatID, "mode": "Dashboard", "clusterID": "1"}, frame["chat"])
}

// The list watch opens with the chats there are, each an Added, closed by the
// Bookmark that says the snapshot is whole. The Bookmark carries no chat: the
// webview detects it by type and drops a change with no entity.
func TestChatsWatchClosesItsSnapshotWithABookmark(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "how many pods?") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	resp, events := chatFrames(t, srv, `subscription { chatsWatch { type chat { id title mode clusterID } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool { return f["type"] == "Added" })
	assert.Equal(t, map[string]any{
		"id": chatID, "title": "how many pods?", "mode": "Chat", "clusterID": "1",
	}, frame["chat"])

	assert.Equal(t, map[string]any{"type": "Bookmark", "chat": nil}, nextChatFrame(t, events))
}

// The transcript watch carries the question and the answer, and finishedAt is null
// while the answer is still streaming and a time once its run has finished.
func TestChatMessagesWatchCarriesTheAnswerToCompletion(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID finishedAt } }`)
	answer := sent["chatSend"].(map[string]any)
	assert.Nil(t, answer["finishedAt"], "a streaming answer has not finished")

	resp, events := chatFrames(t, srv,
		`subscription { chatMessagesWatch(chatID: "`+answer["chatID"].(string)+`") { type message { seq status finishedAt } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	assert.NotNil(t, frame["message"].(map[string]any)["finishedAt"])
}

// A transcript watch serves its own chat's rows alone: the snapshot is the question,
// the answer as far as it has streamed, and the Bookmark; then the answer's changes.
func TestChatMessagesWatchIsScopedAndStreamsChanges(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "first") { chatID } }`)
	// A route, so the first chat's turn cannot take the staged reply.
	second := fake.Route("second")
	second.SetReply(llm.Chunk{Kind: llm.ChunkThinking, Text: "so far"}, llm.Chunk{Text: "done"})
	gate := make(chan struct{})
	second.SetGate(gate)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "second") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)
	query := `subscription { chatMessagesWatch(chatID: "` + chatID + `") { type message { chatID seq role status thinking } } }`

	// Send returns before the turn streams; once the thought is in, the turn is
	// parked at the gate and the next snapshot is fixed.
	first, events := chatFrames(t, srv, query)
	defer first.Body.Close()
	awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["thinking"] == "so far"
	})
	first.Body.Close()

	resp, events := chatFrames(t, srv, query)
	defer resp.Body.Close()
	assert.Equal(t, map[string]any{"type": "Added", "message": map[string]any{
		"chatID": chatID, "seq": float64(0), "role": "User", "status": "Complete", "thinking": "",
	}}, nextChatFrame(t, events))
	assert.Equal(t, map[string]any{"type": "Added", "message": map[string]any{
		"chatID": chatID, "seq": float64(1), "role": "Assistant", "status": "Streaming", "thinking": "so far",
	}}, nextChatFrame(t, events))
	assert.Equal(t, map[string]any{"type": "Bookmark", "message": nil}, nextChatFrame(t, events))

	close(gate)
	for {
		frame := nextChatFrame(t, events)
		require.Equal(t, chatID, frame["message"].(map[string]any)["chatID"])
		if frame["type"] == "Modified" {
			break
		}
	}
}

// A question has no run, so it names no provider and no effort: the provider is
// null rather than a row labelled by an empty id, and the effort is empty.
func TestAQuestionNamesNoProvider(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv,
		`subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") { message { role provider { id } effort } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["role"] == "User"
	})
	assert.Equal(t, map[string]any{"role": "User", "provider": nil, "effort": ""}, frame["message"])
}

// A question carries the cluster card as its first block, where the webview reads
// it. The test server's card source can read nothing, so the card is the
// unavailable one: still a card, still first.
func TestAQuestionCarriesTheClusterCardFirst(t *testing.T) {
	srv := newChatServer(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv,
		`subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") { message { role content } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["role"] == "User"
	})
	blocks := frame["message"].(map[string]any)["content"].([]any)
	require.Len(t, blocks, 2)
	assert.Equal(t, "context", blocks[0].(map[string]any)["type"])
	assert.Contains(t, blocks[0].(map[string]any)["text"], "unavailable")
	assert.Equal(t, map[string]any{"type": "text", "text": "hi"}, blocks[1])
}

// A message whose provider the service no longer holds is labelled by the stored
// id, with no dialect: what was run on is known, what it spoke is not.
func TestAMessageOutlivesItsProvidersRow(t *testing.T) {
	srv, db, _ := newChatServerOver(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	_, err := db.Write.Exec(`UPDATE agent_runs SET provider = 'gone'`)
	require.NoError(t, err)

	resp, events := chatFrames(t, srv,
		`subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") { message { role status provider { id label llmDialect } } } }`)
	defer resp.Body.Close()

	// The settled row, not the live one: while the turn runs the watch serves the
	// overlay, which carries the provider the send named rather than the row's.
	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["role"] == "Assistant" && msg["status"] == "Complete"
	})
	assert.Equal(t, map[string]any{"id": "gone", "label": "gone", "llmDialect": nil}, frame["message"].(map[string]any)["provider"])
}

// A send the service refuses carries its code too: the mutation is the one the
// composer branches on.
func TestARefusedSendCarriesItsCode(t *testing.T) {
	srv := newChatServer(t)

	raw := postGQL(t, srv.URL, `{"query":"mutation { chatSend(chatID: \"`+appdb.NewID()+`\", mode: Chat, clusterID: \"1\", sandboxDisabled: false, providerID: \"fake\", modelID: \"fake\", effort: \"high\", requestID: \"`+appdb.NewID()+`\", content: \"hi\") { id } }"}`)

	assert.Contains(t, string(raw), `"code":"KSTACK_RECORD_NOT_FOUND"`)
}

// A send under a cluster no row holds is refused as not found.
func TestChatSendForwardsAClusterRefusal(t *testing.T) {
	srv := newChatServer(t)

	raw := postGQL(t, srv.URL, `{"query":"mutation { chatSend(mode: Chat, clusterID: \"999\", sandboxDisabled: false, providerID: \"fake\", modelID: \"fake\", effort: \"high\", requestID: \"`+appdb.NewID()+`\", content: \"hi\") { id } }"}`)

	assert.Contains(t, string(raw), `"code":"KSTACK_RECORD_NOT_FOUND"`)
}

// A refusal the table names carries its code, which is the only thing that crosses
// GraphQL: a Go error's identity does not.
func TestAChatRefusalCarriesItsCode(t *testing.T) {
	srv := newChatServer(t)

	raw := postGQL(t, srv.URL, `{"query":"mutation { chatRename(id: \"`+appdb.NewID()+`\", title: \"t\") { id } }"}`)

	assert.Contains(t, string(raw), `"code":"KSTACK_RECORD_NOT_FOUND"`)
}

// Every refusal chatErr maps reaches the client as its code, one case per entry of
// chatRefusals (TestTheChatRefusalTableIsPinned holds the count).
func TestChatRefusalsCarryTheirCode(t *testing.T) {
	send := func(effort string) string {
		return `{"query":"mutation { chatSend(mode: Chat, clusterID: \"1\", sandboxDisabled: false, providerID: \"fake\", modelID: \"fake\", effort: \"` + effort +
			`\", requestID: \"` + appdb.NewID() + `\", content: \"hi\") { id } }"}`
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{chatsvc.ErrBadRequest, "KSTACK_VALIDATION_ERROR"},
		{chatsvc.ErrChatGone, "KSTACK_RECORD_NOT_FOUND"},
		{chatsvc.ErrClusterGone, "KSTACK_RECORD_NOT_FOUND"},
		{chatsvc.ErrTurnInFlight, "KSTACK_CONFLICT"},
		{chatsvc.ErrStopping, "KSTACK_SERVICE_UNAVAILABLE"},
		{chatsvc.ErrChatContextFull, "KSTACK_CHAT_CONTEXT_FULL"},
		{chatsvc.ErrChatSandboxChanged, "KSTACK_CHAT_SANDBOX_CHANGED"},
		{chatsvc.ErrGrantGone, "KSTACK_RECORD_NOT_FOUND"},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			srv := httptest.NewServer(graph.NewServer(&graph.Resolver{ChatSvc: refusingChat{err: tc.err}}))
			defer srv.Close()

			raw := postGQL(t, srv.URL, send("high"))

			assert.Contains(t, string(raw), `"code":"`+tc.code+`"`)
		})
	}

	// Send wraps the catalog's refusal in ErrBadRequest, which chatErr sees through.
	t.Run("an effort the model does not list", func(t *testing.T) {
		raw := postGQL(t, newChatServer(t).URL, send("max"))

		assert.Contains(t, string(raw), `"code":"KSTACK_VALIDATION_ERROR"`)
	})
}

// Two refusals in one request are two errors: gqlgen stamps a path onto the error a
// resolver returns, so a shared value would report the first field for both.
func TestChatRefusalsDoNotShareErrorState(t *testing.T) {
	srv := newChatServer(t)

	raw := postGQL(t, srv.URL, `{"query":"mutation { alpha: chatRename(id: \"`+appdb.NewID()+`\", title: \"t\") { id } beta: chatRename(id: \"`+appdb.NewID()+`\", title: \"t\") { id } }"}`)

	var resp struct {
		Errors []struct {
			Path       []string
			Extensions map[string]any
		}
	}
	require.NoError(t, json.Unmarshal(raw, &resp), "%s", raw)
	require.Len(t, resp.Errors, 2, "%s", raw)
	assert.Equal(t, []string{"alpha"}, resp.Errors[0].Path)
	assert.Equal(t, []string{"beta"}, resp.Errors[1].Path)
	assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", resp.Errors[1].Extensions["code"])
}

// A failure the table does not name stays opaque: a store failure is not something
// a client can act on, so it carries no code of ours.
func TestAFailedChatStoreCarriesNoCode(t *testing.T) {
	srv, db, _ := newChatServerOver(t)
	require.NoError(t, db.Close())

	raw := postGQL(t, srv.URL, `{"query":"mutation { chatDelete(id: \"`+appdb.NewID()+`\") }"}`)

	assert.Contains(t, string(raw), `"errors"`)
	assert.NotContains(t, string(raw), `"code":"KSTACK_`)
}

// Both chat watches go through watchStream, so a read that fails ends the
// subscription with the reason under the watchFailed extension rather than leaving the
// client on a stream that has stopped.
func TestAChatWatchOverAFailedStoreEndsWithItsReason(t *testing.T) {
	srv, db, _ := newChatServerOver(t)
	require.NoError(t, db.Close())

	for _, tc := range []struct{ query, read string }{
		{`subscription { chatsWatch { type } }`, "list chats"},
		{`subscription { chatMessagesWatch(chatID: "` + appdb.NewID() + `") { type } }`, "list messages"},
	} {
		resp, events := chatFrames(t, srv, tc.query)
		ev := testutil.Recv(t, events, "the failure frame")
		assert.Contains(t, ev.data, `"watchFailed":true`)
		assert.Contains(t, ev.data, tc.read)
		resp.Body.Close()
	}
}

// The models query is every provider's catalog under its provider, dialect included.
func TestTheModelsQueryListsEveryProvidersCatalog(t *testing.T) {
	srv := newChatServer(t)

	raw := postGQL(t, srv.URL, `{"query":"{ models { provider { id label llmDialect } id label efforts defaultEffort } }"}`)

	assert.JSONEq(t, `{"data":{"models":[{
		"provider":{"id":"fake","label":"Fake","llmDialect":"Fake"},
		"id":"fake","label":"Fake model","efforts":["low","high"],"defaultEffort":"low"},{
		"provider":{"id":"fake","label":"Fake","llmDialect":"Fake"},
		"id":"fake-no-tools","label":"Fake model (no tools)","efforts":[],"defaultEffort":""}]}}`, string(raw))
}

// securityRefused answers what the settings file's Open left out.
func TestSecurityRefusedIsWhatOpenLeftOut(t *testing.T) {
	refuse := func(*securityconfig.Settings) []securityconfig.Refusal {
		return []securityconfig.Refusal{{Field: "rules", Value: "Bash(rm *)", Reason: "is not a rule"}}
	}
	cfg, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"), securityconfig.WithChecks(refuse))
	require.NoError(t, err)
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{SecurityCfg: securityconfig.NewService(cfg, nil, nil, "")}))
	t.Cleanup(srv.Close)

	raw := postGQL(t, srv.URL, `{"query":"{ securityRefused { field value reason } }"}`)

	assert.JSONEq(t, `{"data":{"securityRefused":[{"field":"rules","value":"Bash(rm *)","reason":"is not a rule"}]}}`, string(raw))
}

// approvalDecide reaches the service, and a decision nothing waits on answers false
// rather than an error.
func TestApprovalDecideReachesTheService(t *testing.T) {
	srv := newChatServer(t)

	data := mutate(t, srv, `mutation { approvalDecide(id: "`+appdb.NewID()+`", approve: true) }`)

	assert.Equal(t, false, data["approvalDecide"])
}

// An answer carries its turn's tool calls off their rows: a call of a tool the turn
// was not offered is listed, not run, with the refusal the model read and what it
// asked for.
func TestAMessageCarriesItsToolCalls(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall("Bash", `{"command":"ls","description":"List files"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID toolCalls { id } } }`)
	answer := sent["chatSend"].(map[string]any)
	assert.Equal(t, []any{}, answer["toolCalls"], "a new answer has none")

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+answer["chatID"].(string)+`") {
		message { seq status toolCalls { toolUseID name arguments status
			action { description command { text cwd background } } approval { id } output isError } } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	assert.Equal(t, []any{map[string]any{
		"toolUseID": "call-1", "name": "Bash", "arguments": map[string]any{"command": "ls", "description": "List files"}, "status": "NotRun",
		"action": map[string]any{
			"description": "List files",
			"command":     map[string]any{"text": "ls", "cwd": "", "background": false},
		},
		"approval": nil, "output": `{"error":"unknown-tool"}`, "isError": true,
	}}, frame["message"].(map[string]any)["toolCalls"])
}

// A call carries its cluster writes in the order asked: one that waits with its
// body and subresource, one decided or abandoned, and a pending one on a call no
// longer running, with neither body nor media type.
func TestToolCallCarriesItsClusterWrites(t *testing.T) {
	srv, db, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall("Bash", `{"command":"kubectl delete pod x"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)
	query := `subscription { chatMessagesWatch(chatID: "` + chatID + `") {
		message { seq status toolCalls { clusterWrites { approval { id status } method path subresource contentType body dryRun } } } } }`
	settled := func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	}
	writes := func() []any {
		resp, events := chatFrames(t, srv, query)
		defer resp.Body.Close()
		f := awaitChatFrame(t, events, settled)
		return f["message"].(map[string]any)["toolCalls"].([]any)[0].(map[string]any)["clusterWrites"].([]any)
	}
	resp, events := chatFrames(t, srv, query)
	awaitChatFrame(t, events, settled)
	resp.Body.Close()

	var callID string
	require.NoError(t, db.Read.QueryRow(`SELECT id FROM tool_calls`).Scan(&callID))
	_, err := db.Write.Exec(`UPDATE tool_calls SET status = 'running', started_at = 1, finished_at = NULL WHERE id = ?`, callID)
	require.NoError(t, err)
	ids := []string{appdb.NewID(), appdb.NewID(), appdb.NewID()}
	for i, row := range []struct{ status, request string }{
		{"approved", `{"method":"DELETE","path":"/api/v1/namespaces/web/pods/a","subresource":"","contentType":"","body":"","dryRun":false}`},
		{"abandoned", `{"method":"PATCH","path":"/apis/apps/v1/namespaces/web/deployments/b?dryRun=All","subresource":"","contentType":"application/merge-patch+json","body":"{}","dryRun":true}`},
		{"pending", `{"method":"POST","path":"/api/v1/namespaces/web/pods/c/eviction","subresource":"eviction","contentType":"application/json","body":"{\"kind\":\"Eviction\"}","dryRun":false}`},
	} {
		_, err := db.Write.Exec(`INSERT INTO approvals (id, tool_call_id, kind, request, status, created_at) VALUES (?, ?, 'cluster', ?, ?, ?)`,
			ids[i], callID, row.request, row.status, 10+i)
		require.NoError(t, err)
	}

	assert.Equal(t, []any{
		map[string]any{"approval": map[string]any{"id": ids[0], "status": "Approved"}, "method": "DELETE", "path": "/api/v1/namespaces/web/pods/a",
			"subresource": "", "contentType": "", "body": "", "dryRun": false},
		map[string]any{"approval": map[string]any{"id": ids[1], "status": "Abandoned"}, "method": "PATCH", "path": "/apis/apps/v1/namespaces/web/deployments/b?dryRun=All",
			"subresource": "", "contentType": "", "body": "", "dryRun": true},
		map[string]any{"approval": map[string]any{"id": ids[2], "status": "Pending"}, "method": "POST", "path": "/api/v1/namespaces/web/pods/c/eviction",
			"subresource": "eviction", "contentType": "application/json", "body": `{"kind":"Eviction"}`, "dryRun": false},
	}, writes())

	_, err = db.Write.Exec(`UPDATE tool_calls SET status = 'failed', error = '{"error":"stranded"}', finished_at = 2 WHERE id = ?`, callID)
	require.NoError(t, err)
	stranded := writes()[2].(map[string]any)
	assert.Equal(t, "", stranded["body"], "a pending write on a call no longer running does not wait")
	assert.Equal(t, "", stranded["contentType"])
}

// A memory call's action carries who the note reaches, which is what the approval
// request asks about.
func TestAMessageCarriesAMemoryCallsScope(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall("Memory", `{"op":"save","name":"prefs","body":"b","scope":"everywhere"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") {
		message { seq status toolCalls { action { memory { op name body scope } } } } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	assert.Equal(t, []any{map[string]any{
		"action": map[string]any{"memory": map[string]any{"op": "save", "name": "prefs", "body": "b", "scope": "everywhere"}},
	}}, frame["message"].(map[string]any)["toolCalls"])
}

// A search is the provider's call, drawn by its query, and the answer carries what
// its text cites.
func TestAMessageCarriesItsSearchesAndCitations(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetReply(
		llm.Chunk{Kind: llm.ChunkServer, Call: llm.ServerUseBlock("srv_1", anthropicwebsearch.Name, json.RawMessage(`{"query":"kubernetes 1.36"}`))},
		llm.Chunk{Text: "It shipped."},
	)
	fake.SetCitations(llm.Citation{Type: "web_search_result_location", URL: "https://kubernetes.io/releases", Title: "Releases", CitedText: "1.36"})
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID citations { url } } }`)
	answer := sent["chatSend"].(map[string]any)
	assert.Equal(t, []any{}, answer["citations"], "a new answer cites nothing")

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+answer["chatID"].(string)+`") {
		message { seq status toolCalls { name contract status runsOn action { search { query } } } citations { type url title citedText } } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	msg := frame["message"].(map[string]any)
	assert.Equal(t, []any{map[string]any{
		"name": "anthropic_web_search_20260318", "contract": "web_search_20260318", "status": nil, "runsOn": "Provider",
		"action": map[string]any{"search": map[string]any{"query": "kubernetes 1.36"}},
	}}, msg["toolCalls"])
	assert.Equal(t, []any{map[string]any{
		"type": "web_search_result_location", "url": "https://kubernetes.io/releases", "title": "Releases", "citedText": "1.36",
	}}, msg["citations"])
}

// Every kind is served as its enum value: gqlgen serves a kind the binding lacks
// as "", with no error.
func TestEveryActionKindIsServed(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetReply(llm.Chunk{Kind: llm.ChunkServer, Call: llm.ServerUseBlock("srv_1", anthropicwebsearch.Name, json.RawMessage(`{"query":"q"}`))})
	fake.SetToolCalls(
		llm.StagedCall(bash.Name, `{"command":"ls"}`),
		llm.StagedCall(read.Name, `{"file_path":"/a"}`),
		llm.StagedCall(write.Name, `{"file_path":"/a","content":""}`),
		llm.StagedCall(edit.Name, `{"file_path":"/a","old_string":"a","new_string":"b"}`),
		llm.StagedCall(webfetch.Name, `{"url":"https://a.test/"}`),
		llm.StagedCall(taskstop.Name, `{"task_id":"t"}`),
		llm.StagedCall(memory.Name, `{"op":"forget","name":"a"}`),
		llm.StagedCall(agenttool.Name, `{"description":"d","prompt":"p"}`),
		llm.StagedCall(kubequery.Name, `{"sql":"SELECT 1"}`),
	)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") {
		message { seq status toolCalls { actionKind } } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	var served []any
	for _, c := range frame["message"].(map[string]any)["toolCalls"].([]any) {
		served = append(served, c.(map[string]any)["actionKind"])
	}
	var want []any
	for _, k := range tools.ActionKinds {
		want = append(want, enumOfKind[k])
	}
	assert.ElementsMatch(t, want, served)
}

// enumOfKind is each kind's ToolActionKind value. kube-query is KubeQuery, not
// the kind with its first letter capitalised.
var enumOfKind = map[tools.ActionKind]string{
	tools.ActionCommand: "Command", tools.ActionRead: "Read", tools.ActionWrite: "Write", tools.ActionEdit: "Edit",
	tools.ActionSearch: "Search", tools.ActionFetch: "Fetch", tools.ActionStop: "Stop", tools.ActionMemory: "Memory",
	tools.ActionDelegate: "Delegate", tools.ActionKubeQuery: "KubeQuery",
}

// A subagent's call is served in its parent's answer, naming the Agent call it ran
// under; the answer's own calls name none.
func TestASubagentsCallIsServedUnderItsAgentCall(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall(agenttool.Name, `{"description":"d","prompt":"p"}`))
	fake.Route("p").SetToolCalls(llm.StagedCall(taskstop.Name, `{"task_id":"t"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") {
		message { seq status toolCalls { id name agentCallID action { delegate { prompt agentType model } } } } } }`)
	defer resp.Body.Close()

	// The subagent runs in the background, so its call can land after the answer settles.
	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete" && len(msg["toolCalls"].([]any)) == 2
	})
	calls := frame["message"].(map[string]any)["toolCalls"].([]any)
	agent, sub := calls[0].(map[string]any), calls[1].(map[string]any)
	assert.Nil(t, agent["agentCallID"])
	assert.Equal(t, map[string]any{"delegate": map[string]any{"prompt": "p", "agentType": "general-purpose", "model": ""}}, agent["action"])
	assert.Equal(t, taskstop.Name, sub["name"])
	assert.Equal(t, agent["id"], sub["agentCallID"])
}

// A call that started a background task carries the task off its row: its status,
// and the exit code once it exited with one; its action says whether it asked to
// run in the background.
func TestAToolCallCarriesItsBackgroundTask(t *testing.T) {
	srv, db, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall("Bash", `{"command":"make serve","run_in_background":true}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)
	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+chatID+`") { message { seq status } } }`)
	awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	resp.Body.Close()
	for _, q := range []string{
		`INSERT INTO approvals (id, tool_call_id, status, created_at) SELECT 'a', id, 'approved', 0 FROM tool_calls`,
		`INSERT INTO background_tasks (id, chat_id, tool_call_id, output_path, status, exit_code, started_at, finished_at)
		 SELECT 'task-1', '` + chatID + `', id, '/r/task-1.output', 'exited', 3, 0, 0 FROM tool_calls`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}

	resp, events = chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+chatID+`") {
		message { seq toolCalls { action { command { background } } approval { status } background { status exitCode } } } } }`)
	defer resp.Body.Close()
	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1)
	})
	assert.Equal(t, []any{map[string]any{
		"action":     map[string]any{"command": map[string]any{"background": true}},
		"approval":   map[string]any{"status": "Approved"},
		"background": map[string]any{"status": "Exited", "exitCode": float64(3)},
	}}, frame["message"].(map[string]any)["toolCalls"])
}

// backgroundTaskStop reaches the service, and a call that started no running
// task answers false rather than an error.
func TestBackgroundTaskStopReachesTheService(t *testing.T) {
	srv := newChatServer(t)

	data := mutate(t, srv, `mutation { backgroundTaskStop(id: "`+appdb.NewID()+`") }`)

	assert.Equal(t, false, data["backgroundTaskStop"])
}

// awaitChatFrame reads frames until one satisfies want, and returns it.
func awaitChatFrame(t *testing.T, events <-chan sseEvent, want func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(testutil.Timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream closed before the frame arrived")
			}
			if f := chatFrameOf(t, ev); f != nil && want(f) {
				return f
			}
		case <-deadline:
			t.Fatal("timed out waiting for a frame")
		}
	}
}

// firstChatFrameType is the type of the first frame the stream carries.
func firstChatFrameType(t *testing.T, events <-chan sseEvent) string {
	t.Helper()
	return nextChatFrame(t, events)["type"].(string)
}

// nextChatFrame is the next watch frame, whatever it holds.
func nextChatFrame(t *testing.T, events <-chan sseEvent) map[string]any {
	t.Helper()
	return awaitChatFrame(t, events, func(map[string]any) bool { return true })
}

// chatFrameOf is the one watch frame an event carries, or nil for a frame that is
// not one (gqlgen's keep-alives and its terminal event).
func chatFrameOf(t *testing.T, ev sseEvent) map[string]any {
	t.Helper()
	var payload struct {
		Data   map[string]any
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
		return nil
	}
	require.Empty(t, payload.Errors, "%s", ev.data)
	for _, v := range payload.Data {
		if f, ok := v.(map[string]any); ok {
			return f
		}
	}
	return nil
}

// --- memory ---

type noUIDs struct{}

func (noUIDs) ServerUID(context.Context, apimeta.ClusterID) string { return "" }

// newMemoryServer is a GraphQL server over a real memory service, with clusters
// "1" and "2" seeded.
func newMemoryServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _ := newMemoryServerOver(t)
	return srv
}

// newMemoryServerOver is newMemoryServer handing back the app.db, for a test that
// fails the store by closing it, and the service's stop.
func newMemoryServerOver(t *testing.T) (*httptest.Server, *appdb.DB, func(context.Context) error) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"1", "2"} {
		_, err = db.Write.Exec(`INSERT INTO clusters (id, source, source_key, created_at, updated_at) VALUES (?, 'kubeconfig', ?, 0, 0)`, id, "ctx-"+id)
		require.NoError(t, err)
	}
	memorySvc, err := memorysvc.New(db, noUIDs{})
	require.NoError(t, err)
	stop, err := memorySvc.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, stop(context.Background()))
		require.NoError(t, memorySvc.Close())
	})
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{MemorySvc: memorySvc}))
	t.Cleanup(srv.Close)
	return srv, db, stop
}

// A failure the table does not name stays opaque, and a watch opened as the
// service stops is refused as unavailable.
func TestAFailedMemoryStoreCarriesNoCode(t *testing.T) {
	srv, db, stop := newMemoryServerOver(t)
	require.NoError(t, db.Close())

	body, _ := json.Marshal(map[string]string{"query": memorySave(`clusterID: "1", name: "n", body: "b"`)})
	raw := string(postGQL(t, srv.URL, string(body)))
	assert.Contains(t, raw, `"errors"`)
	assert.NotContains(t, raw, `"code":"KSTACK_`)

	require.NoError(t, stop(t.Context()))
	body, _ = json.Marshal(map[string]string{"query": `subscription { memoriesWatch(clusterID: "1") { type } }`})
	assert.Contains(t, string(postGQL(t, srv.URL, string(body))), `"code":"KSTACK_SERVICE_UNAVAILABLE"`)
}

// memorySave is the mutation for one input, as GraphQL text.
func memorySave(input string) string {
	return `mutation { memorySave(input: {` + input + `}) { id clusterID name body writtenBy } }`
}

func TestMemorySaveCreatesAndRewrites(t *testing.T) {
	srv := newMemoryServer(t)

	data := mutate(t, srv, memorySave(`clusterID: "1", name: "pages", body: "b"`))
	created := data["memorySave"].(map[string]any)
	id := created["id"].(string)
	delete(created, "id")
	assert.Equal(t, map[string]any{
		"clusterID": "1", "name": "pages", "body": "b",
		"writtenBy": "User",
	}, created)

	data = mutate(t, srv, memorySave(`id: "`+id+`", clusterID: null, name: "renamed", body: "b2"`))
	assert.Equal(t, "renamed", data["memorySave"].(map[string]any)["name"])
	assert.Nil(t, data["memorySave"].(map[string]any)["clusterID"])

	assert.Equal(t, map[string]any{"memoryDelete": true}, mutate(t, srv, `mutation { memoryDelete(id: "`+id+`") }`))
}

// Each refusal carries its code; the dialog branches on the code alone.
func TestAMemoryRefusalCarriesItsCode(t *testing.T) {
	srv := newMemoryServer(t)
	mutate(t, srv, memorySave(`clusterID: "1", name: "pages", body: "b"`))
	mutate(t, srv, memorySave(`clusterID: "2", name: "theirs", body: "b"`))

	for code, op := range map[string]string{
		"KSTACK_MEMORY_NAME_TAKEN": memorySave(`clusterID: "2", name: "theirs", body: "b"`),
		"KSTACK_MEMORY_SECRET":     memorySave(`clusterID: "1", name: "s", body: "sk-ant-0123456789abcdefghij"`),
		"KSTACK_VALIDATION_ERROR":  memorySave(`clusterID: "1", name: "Bad Name", body: "b"`),
		"KSTACK_RECORD_NOT_FOUND":  `mutation { memoryDelete(id: "` + appdb.NewID() + `") }`,
	} {
		t.Run(code, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"query": op})
			raw := postGQL(t, srv.URL, string(body))
			assert.Contains(t, string(raw), `"code":"`+code+`"`)
		})
	}

	full := strings.Repeat("b", memorysvc.BodyMax)
	for i := range 20 {
		body, _ := json.Marshal(map[string]string{"query": memorySave(`clusterID: "1", name: "n` + strconv.Itoa(i) + `", body: "` + full + `"`)})
		raw := string(postGQL(t, srv.URL, string(body)))
		if strings.Contains(raw, `"errors"`) {
			assert.Contains(t, raw, `"code":"KSTACK_MEMORY_FULL"`)
			return
		}
	}
	t.Fatal("the scope never filled")
}

func TestMemoriesWatchOpensWithASnapshot(t *testing.T) {
	srv := newMemoryServer(t)
	mutate(t, srv, memorySave(`clusterID: "1", name: "pages", body: "b"`))
	mutate(t, srv, memorySave(`clusterID: "2", name: "theirs", body: "b"`))

	resp, events := chatFrames(t, srv, `subscription { memoriesWatch(clusterID: "1") { type memory { name } } }`)
	defer resp.Body.Close()

	first := awaitChatFrame(t, events, func(map[string]any) bool { return true })
	assert.Equal(t, map[string]any{"type": "Added", "memory": map[string]any{"name": "pages"}}, first)
	assert.Equal(t, "Bookmark", firstChatFrameType(t, events))
}

// An agent's task serves its status and report, and an answer and its chat say
// when a run under them waits on the user.
func TestAnAgentsTaskAndWaitAreServed(t *testing.T) {
	srv, db, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall("Bash", `{"command":"make serve"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)
	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+chatID+`") { message { seq status } } }`)
	awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	resp.Body.Close()
	for _, q := range []string{
		`INSERT INTO agent_runs (id, parent_run_id, agent_type, app_version, trigger, chat_id, provider, model, dialect, task, result, status, created_at)
		 SELECT 'sub', run_id, 'general-purpose', 'test', 'agent', chat_id, 'fake', 'fake', 'fake', 'p', 'Two pods.', 'waiting_approval', 0
		 FROM messages WHERE seq = 1`,
		`UPDATE tool_calls SET spawned_run_id = 'sub'`,
		`INSERT INTO background_tasks (id, chat_id, tool_call_id, output_path, status, started_at, finished_at)
		 SELECT 'task-1', '` + chatID + `', id, '/r/task-1.output', 'completed', 0, 0 FROM tool_calls`,
	} {
		_, err := db.Write.Exec(q)
		require.NoError(t, err, q)
	}

	resp, events = chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+chatID+`") {
		message { seq awaitingApproval toolCalls { background { status report } } } } }`)
	defer resp.Body.Close()
	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1)
	})
	msg := frame["message"].(map[string]any)
	assert.Equal(t, true, msg["awaitingApproval"])
	assert.Equal(t, []any{map[string]any{
		"background": map[string]any{"status": "Completed", "report": "Two pods."},
	}}, msg["toolCalls"])

	list, chats := chatFrames(t, srv, `subscription { chatsWatch { chat { id awaitingApproval } } }`)
	defer list.Body.Close()
	awaitChatFrame(t, chats, func(f map[string]any) bool {
		chat, ok := f["chat"].(map[string]any)
		return ok && chat["id"] == chatID && chat["awaitingApproval"] == true
	})
}

// A KubeQuery call serves the statement as it runs and the limit as read, beside the
// model's description.
func TestAKubeQueryCallServesItsQuery(t *testing.T) {
	srv, _, fake := newChatServerOver(t)
	fake.SetToolCalls(llm.StagedCall(kubequery.Name, `{"sql":"SELECT 1;","limit":5,"description":"Count the pods"}`))
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)

	resp, events := chatFrames(t, srv, `subscription { chatMessagesWatch(chatID: "`+sent["chatSend"].(map[string]any)["chatID"].(string)+`") {
		message { seq status toolCalls { action { description kubeQuery { sql limit } } } } } }`)
	defer resp.Body.Close()

	frame := awaitChatFrame(t, events, func(f map[string]any) bool {
		msg, ok := f["message"].(map[string]any)
		return ok && msg["seq"] == float64(1) && msg["status"] == "Complete"
	})
	assert.Equal(t, []any{map[string]any{"action": map[string]any{
		"description": "Count the pods", "kubeQuery": map[string]any{"sql": "SELECT 1", "limit": float64(5)},
	}}}, frame["message"].(map[string]any)["toolCalls"])
}

// The sandbox query answers the status the app built, reason and all.
func TestTheSandboxQueryAnswersTheStatus(t *testing.T) {
	srv := newSandboxedChatServer(t, sandbox.Status{Available: true, Reason: "bwrap at /usr/bin/bwrap"})

	data := mutate(t, srv, `{ sandbox { available reason } }`)

	assert.Equal(t, map[string]any{"available": true, "reason": "bwrap at /usr/bin/bwrap"}, data["sandbox"])
}

// The switch answers the chat it committed, and is refused on a machine with no
// sandbox.
func TestChatSandboxDisabledSetServesTheSwitchedChat(t *testing.T) {
	srv := newSandboxedChatServer(t, sandbox.Status{Available: true})
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)

	data := mutate(t, srv, `mutation { chatSandboxDisabledSet(id: "`+chatID+`", sandboxDisabled: true) { id sandboxDisabled } }`)

	assert.Equal(t, map[string]any{"id": chatID, "sandboxDisabled": true}, data["chatSandboxDisabledSet"])

	none := newChatServer(t)
	sent = mutate(t, none, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID = sent["chatSend"].(map[string]any)["chatID"].(string)
	raw := postGQL(t, none.URL, `{"query":"mutation { chatSandboxDisabledSet(id: \"`+chatID+`\", sandboxDisabled: true) { id } }"}`)
	assert.Contains(t, string(raw), `"code":"KSTACK_VALIDATION_ERROR"`)
}

// sandboxPathServer is a server over a Service whose sync reads open as every
// run's, with pending outside it, and whose refresh answers resolve; with no
// sandbox the Service has neither.
func sandboxPathServer(t *testing.T, available bool, resolve func(context.Context) ([]string, error)) (srv *httptest.Server, open, pending string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	open, pending = filepath.Join(base, "open"), filepath.Join(base, "pending")
	require.NoError(t, os.Mkdir(open, 0o755))
	require.NoError(t, os.Mkdir(pending, 0o755))
	store, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	svc := securityconfig.NewService(store, nil, nil, "")
	if available {
		zones := func() securityconfig.Zones {
			return securityconfig.Zones{Open: sandbox.FilePolicy{Read: []string{open}}}
		}
		svc = securityconfig.NewService(store, zones, resolve, "timeout")
		require.NoError(t, svc.SyncPath(t.Context(), []string{open, pending}))
	}
	srv = httptest.NewServer(graph.NewServer(&graph.Resolver{SecurityCfg: svc, SandboxStatus: sandbox.Status{Available: available}}))
	t.Cleanup(srv.Close)
	return srv, open, pending
}

// refusal is errs' one error's code and message.
func refusal(t *testing.T, errs []gqlError) (code, message string) {
	t.Helper()
	require.Len(t, errs, 1)
	code, _ = errs[0].Extensions["code"].(string)
	return code, errs[0].Message
}

func TestSandboxPathMutationsAnswerTheList(t *testing.T) {
	srv, open, pending := sandboxPathServer(t, true, func(context.Context) ([]string, error) {
		return nil, errors.New("no shell")
	})
	entry := func(dir, state, source string) map[string]any {
		return map[string]any{"dir": dir, "target": dir, "state": state, "source": source, "shared": false}
	}
	const fields = `{ dir target state source shared }`

	data, _ := mutation(t, srv.URL, `{ sandboxPath `+fields+` sandboxPathFault sandboxPathResolved }`)
	assert.Equal(t, []any{entry(open, "Adopted", "Shell"), entry(pending, "Pending", "Shell")}, data["sandboxPath"])
	assert.Equal(t, "timeout", data["sandboxPathFault"])
	assert.Equal(t, true, data["sandboxPathResolved"])

	data, _ = mutation(t, srv.URL, `mutation { sandboxPathInclude(dir: "`+jsonEscape(pending)+`", target: "`+jsonEscape(pending)+`") `+fields+` }`)
	assert.Equal(t, []any{entry(open, "Adopted", "Shell"), entry(pending, "Adopted", "User")}, data["sandboxPathInclude"])
	data, _ = mutation(t, srv.URL, `mutation { sandboxPathRemove(dir: "`+jsonEscape(open)+`") `+fields+` }`)
	assert.Equal(t, []any{entry(open, "Gone", "User"), entry(pending, "Adopted", "User")}, data["sandboxPathRemove"])

	for query, message := range map[string]string{
		`mutation { sandboxPathInclude(dir: "` + jsonEscape(pending) + `", target: "` + jsonEscape(pending) + `") { dir } }`: "That folder is already included.",
		`mutation { sandboxPathInclude(dir: "` + jsonEscape(pending) + `", target: "/elsewhere") { dir } }`:                  "That folder now leads somewhere else. Check it, then include it again.",
		`mutation { sandboxPathRemove(dir: "/nowhere") { dir } }`:                                                            "That folder is not in the list.",
		`mutation { sandboxPathRefresh { dir } }`:                                                                            "Your shell did not answer: no shell.",
	} {
		_, errs := mutation(t, srv.URL, query)
		code, got := refusal(t, errs)
		assert.Equal(t, "KSTACK_VALIDATION_ERROR", code, query)
		assert.Equal(t, message, got, query)
	}
	data, _ = mutation(t, srv.URL, `{ sandboxPathFault }`)
	assert.Equal(t, "no shell", data["sandboxPathFault"], "the refresh's fault replaces the launch's")
}

// A machine with no sandbox answers an empty list and no fault, and refuses
// every change.
func TestSandboxPathIsEmptyWithNoSandbox(t *testing.T) {
	srv, open, _ := sandboxPathServer(t, false, nil)

	data, _ := mutation(t, srv.URL, `{ sandboxPath { dir } sandboxPathFault sandboxPathResolved }`)
	assert.Equal(t, []any{}, data["sandboxPath"])
	assert.Nil(t, data["sandboxPathFault"])
	assert.Equal(t, false, data["sandboxPathResolved"])
	for _, query := range []string{
		`mutation { sandboxPathInclude(dir: "` + jsonEscape(open) + `", target: "` + jsonEscape(open) + `") { dir } }`,
		`mutation { sandboxPathRemove(dir: "` + jsonEscape(open) + `") { dir } }`,
		`mutation { sandboxPathRefresh { dir } }`,
	} {
		_, errs := mutation(t, srv.URL, query)
		code, _ := refusal(t, errs)
		assert.Equal(t, "KSTACK_VALIDATION_ERROR", code, query)
	}
}

// jsonEscape is s as the inside of a GraphQL string literal.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// newPermissionServer is a server over the default cluster fixtures and a
// security store over file, which holds body when it is not empty.
func newPermissionServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	file := filepath.Join(t.TempDir(), "security.json")
	if body != "" {
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
	}
	cfg, err := securityconfig.Open(file)
	require.NoError(t, err)
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{ClusterSvc: newFakeClusterService(clusterFixtures()), SecurityCfg: securityconfig.NewService(cfg, nil, nil, "")}))
	t.Cleanup(srv.Close)
	return srv
}

const permissionSettingsFields = `defaultMode contexts { context mode source pattern own }
	rules { effect class context namespace verb group kind line } held`

// refusalOf posts query and answers its one error's message and code.
func refusalOf(t *testing.T, srv *httptest.Server, query string) (string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	raw := postGQL(t, srv.URL, string(body))
	var resp struct {
		Errors []struct {
			Message    string
			Extensions struct{ Code string }
		}
	}
	require.NoError(t, json.Unmarshal(raw, &resp), "%s", raw)
	require.Len(t, resp.Errors, 1, "%s", raw)
	return resp.Errors[0].Message, resp.Errors[0].Extensions.Code
}

// permissionSettings answers each known context's mode and where it comes
// from, the rules, and the class 5 list in words.
func TestPermissionSettingsShowsModesAndRules(t *testing.T) {
	srv := newPermissionServer(t, `{"modes": [{"context": "prod", "mode": "read-only"}], "rules": [{"id": "r", "effect": "deny", "class": 5,
		"context": "prod*", "verb": "delete", "group": "core", "kind": "namespaces"}]}`)

	data := mutate(t, srv, `{ permissionSettings { `+permissionSettingsFields+` destructive } }`)

	got := data["permissionSettings"].(map[string]any)
	assert.Equal(t, "Ask", got["defaultMode"])
	assert.Equal(t, []any{
		map[string]any{"context": "prod", "mode": "ReadOnly", "source": "Entry", "pattern": "prod", "own": true},
		map[string]any{"context": "staging", "mode": "Ask", "source": "Default", "pattern": "", "own": false},
	}, got["contexts"])
	assert.Equal(t, []any{map[string]any{
		"effect": "Deny", "class": "Destructive", "context": "prod*", "namespace": "",
		"verb": "delete", "group": "core", "kind": "namespaces",
		"line": "Deny destructive delete of core namespaces in prod*",
	}}, got["rules"])
	assert.NotEmpty(t, got["destructive"])
	assert.Equal(t, []any{}, got["held"])
}

// Each mutation writes the settings and answers them as they now stand.
func TestPermissionMutationsWriteTheSettings(t *testing.T) {
	srv := newPermissionServer(t, "")

	data := mutate(t, srv, `mutation { permissionModeSet(context: "prod", mode: Ask) { contexts { context mode source pattern } } }`)
	assert.Equal(t, map[string]any{"context": "prod", "mode": "Ask", "source": "Entry", "pattern": "prod"},
		data["permissionModeSet"].(map[string]any)["contexts"].([]any)[0])

	data = mutate(t, srv, `mutation { permissionRuleAdd(input: {effect: Allow, class: UpstreamWrite, context: "staging", namespace: "web"}) {
		rules { id line } } }`)
	rules := data["permissionRuleAdd"].(map[string]any)["rules"].([]any)
	require.Len(t, rules, 1)
	added := rules[0].(map[string]any)
	assert.Equal(t, "Allow cluster writes in staging / web", added["line"])
	assert.NoError(t, appdb.ValidateUUID(added["id"].(string)), "the resolver mints the id")

	data = mutate(t, srv, `mutation { permissionRuleRemove(id: "`+added["id"].(string)+`") { rules { id } } }`)
	assert.Empty(t, data["permissionRuleRemove"].(map[string]any)["rules"])

	data = mutate(t, srv, `mutation { permissionModeClear(context: "prod") { contexts { context source } } }`)
	assert.Equal(t, map[string]any{"context": "prod", "source": "Default"}, data["permissionModeClear"].(map[string]any)["contexts"].([]any)[0])

	data = mutate(t, srv, `mutation { permissionDefaultModeSet(mode: Auto) { defaultMode contexts { context mode } } }`)
	set := data["permissionDefaultModeSet"].(map[string]any)
	assert.Equal(t, "Auto", set["defaultMode"])
	assert.Equal(t, map[string]any{"context": "staging", "mode": "Auto"}, set["contexts"].([]any)[1])
}

// A rule Kstack cannot apply, an edit of a field the file holds unread, and a
// discard of a field that holds nothing unread are validation errors saying
// why; a discard ends a hold.
func TestAPermissionRefusalIsAValidationError(t *testing.T) {
	srv := newPermissionServer(t, "")
	message, code := refusalOf(t, srv, `mutation { permissionRuleAdd(input: {effect: Allow, class: Destructive}) { held } }`)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", code)
	assert.Contains(t, message, "allows a destructive write")
	_, code = refusalOf(t, srv, `mutation { permissionDiscardRefused(field: "rules") { held } }`)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", code)
	_, code = refusalOf(t, srv, `mutation { permissionRuleRemove(id: "nope") { held } }`)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", code)

	srv = newPermissionServer(t, `{"rules": [{"id": "b", "effect": "deny", "class": 9}]}`)
	data := mutate(t, srv, `{ permissionSettings { held } }`)
	assert.Equal(t, []any{"rules"}, data["permissionSettings"].(map[string]any)["held"])
	message, code = refusalOf(t, srv, `mutation { permissionRuleAdd(input: {effect: Deny, class: UpstreamWrite}) { held } }`)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", code)
	assert.Contains(t, message, "cannot read")

	data = mutate(t, srv, `mutation { permissionDiscardRefused(field: "rules") { held } }`)
	assert.Equal(t, []any{}, data["permissionDiscardRefused"].(map[string]any)["held"])
}

// A default mode the file holds that Kstack cannot read is held until the user
// sets one.
func TestADefaultModeIsHeldUntilItIsSet(t *testing.T) {
	srv := newPermissionServer(t, `{"defaultMode": "readonly"}`)
	data := mutate(t, srv, `{ permissionSettings { held } }`)
	assert.Equal(t, []any{"defaultMode"}, data["permissionSettings"].(map[string]any)["held"])

	data = mutate(t, srv, `mutation { permissionDefaultModeSet(mode: Auto) { held } }`)
	assert.Equal(t, []any{}, data["permissionDefaultModeSet"].(map[string]any)["held"])
}

// The settings read the known contexts off the cluster service, so a list it
// cannot answer fails the query rather than drawing no contexts.
func TestPermissionSettingsFailsWithTheClusterList(t *testing.T) {
	cfg, err := securityconfig.Open(filepath.Join(t.TempDir(), "security.json"))
	require.NoError(t, err)
	svc := newFakeClusterService(clusterFixtures())
	svc.listErr = errors.New("store closed")
	srv := httptest.NewServer(graph.NewServer(&graph.Resolver{ClusterSvc: svc, SecurityCfg: securityconfig.NewService(cfg, nil, nil, "")}))
	t.Cleanup(srv.Close)

	_, code := refusalOf(t, srv, `{ permissionSettings { held } }`)

	assert.NotEqual(t, "KSTACK_VALIDATION_ERROR", code)
}

// A rule of a class nothing decides — a Secret read, a new host — is refused,
// so a saved rule always acts.
func TestARuleNothingDecidesIsRefused(t *testing.T) {
	srv := newPermissionServer(t, "")
	for _, input := range []string{
		`{effect: Allow, class: SecretRead}`,
		`{effect: Allow, class: NewHost, context: "dev"}`,
	} {
		message, code := refusalOf(t, srv, `mutation { permissionRuleAdd(input: `+input+`) { held } }`)
		assert.Equal(t, "KSTACK_VALIDATION_ERROR", code, input)
		assert.Contains(t, message, "names a class no rule decides", input)
	}
	data := mutate(t, srv, `{ permissionSettings { rules { id } } }`)
	assert.Empty(t, data["permissionSettings"].(map[string]any)["rules"], "nothing was written")
}

// chatGrants lists a chat's rules by their lines, and chatGrantRemove answers
// the list without the one removed; an id the chat does not hold and a chat
// that is gone are each not found.
func TestChatGrantsListAndRemove(t *testing.T) {
	srv, db, _ := newChatServerOver(t)
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	chatID := sent["chatSend"].(map[string]any)["chatID"].(string)
	id := appdb.NewID()
	_, err := db.Write.Exec(`INSERT INTO chat_grants (id, chat_id, rule, created_at) VALUES (?, ?, ?, 1)`,
		id, chatID, `{"id":"`+id+`","effect":"allow","class":4,"context":"dev","namespace":"web"}`)
	require.NoError(t, err)

	data := mutate(t, srv, `query { chatGrants(chatID: "`+chatID+`") { id line } }`)
	assert.Equal(t, []any{map[string]any{"id": id, "line": "Allow cluster writes in dev / web"}}, data["chatGrants"])

	_, code := refusalOf(t, srv, `mutation { chatGrantRemove(chatID: "`+chatID+`", id: "`+appdb.NewID()+`") { id } }`)
	assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", code)
	data = mutate(t, srv, `mutation { chatGrantRemove(chatID: "`+chatID+`", id: "`+id+`") { id } }`)
	assert.Equal(t, []any{}, data["chatGrantRemove"])

	_, code = refusalOf(t, srv, `mutation { chatGrantRemove(chatID: "`+appdb.NewID()+`", id: "`+id+`") { id } }`)
	assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", code)
}
