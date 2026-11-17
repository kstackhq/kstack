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

package bash

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// get sends d a request for host carrying token, and answers its status and
// body.
func get(t *testing.T, d http.Handler, method, host, token string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+host+"/x", nil)
	r.Host = host
	if token != "" {
		r.Header.Set(tokenHeader, token)
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// Each host reaches its own handler; anything else is the cluster grant's
// 401, whatever its host or method.
func TestTheDoorRoutesByHost(t *testing.T) {
	aws := mountFor(t, newFakeProvider(permissions.AWS, "127.0.0.1:6443"))
	gcp := mountFor(t, newFakeProvider(permissions.GCP, "storage.gcp.kstack.invalid"))
	cluster := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "cluster") })
	d, err := newDoor(cluster, []CloudMount{aws, gcp}, switchesOn(permissions.AWS, permissions.GCP).On)
	require.NoError(t, err)

	_, body := get(t, d, http.MethodGet, "cluster.kstack.invalid", "")
	assert.Equal(t, "cluster", body)
	code, body := get(t, d, http.MethodGet, "127.0.0.1:6443", aws.Grant.Token())
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "read aws", body)
	_, body = get(t, d, http.MethodGet, "storage.gcp.kstack.invalid", gcp.Grant.Token())
	assert.Equal(t, "read gcp", body)

	_, want := get(t, d, http.MethodGet, "unknown.kstack.invalid", "")
	assert.Contains(t, want, "kstack: unauthorized")
	for name, req := range map[string][2]string{
		"an unknown host":   {http.MethodGet, "unknown.kstack.invalid"},
		"localhost":         {http.MethodGet, "localhost:6443"},
		"a CONNECT":         {http.MethodConnect, "127.0.0.1:6443"},
		"the cluster's own": {http.MethodConnect, "cluster.kstack.invalid"},
	} {
		code, body := get(t, d, req[0], req[1], aws.Grant.Token())
		assert.Equal(t, http.StatusUnauthorized, code, name)
		assert.Equal(t, want, body, name)
	}
}

// A run with no cluster still answers the cluster's host 401.
func TestTheDoorWithNoClusterRefusesItsHost(t *testing.T) {
	d, err := newDoor(nil, []CloudMount{mountFor(t, newFakeProvider(permissions.AWS, "127.0.0.1:6443"))}, switchesOn(permissions.AWS).On)
	require.NoError(t, err)
	code, _ := get(t, d, http.MethodGet, "cluster.kstack.invalid", "")
	assert.Equal(t, http.StatusUnauthorized, code)
}

// The door is not trusted: one provider's token on another's route is that
// grant's refusal.
func TestAWrongRouteIsUnauthorized(t *testing.T) {
	aws := mountFor(t, newFakeProvider(permissions.AWS, "127.0.0.1:6443"))
	gcp := mountFor(t, newFakeProvider(permissions.GCP, "storage.gcp.kstack.invalid"))
	d, err := newDoor(nil, []CloudMount{aws, gcp}, switchesOn(permissions.AWS, permissions.GCP).On)
	require.NoError(t, err)
	code, _ := get(t, d, http.MethodGet, "storage.gcp.kstack.invalid", aws.Grant.Token())
	assert.Equal(t, http.StatusUnauthorized, code)
}

// A route reads its switch on every request: off, its host is the door's
// 401; on again, it is served.
func TestSwitchingAProviderOffClosesItsRoute(t *testing.T) {
	aws := mountFor(t, newFakeProvider(permissions.AWS, "127.0.0.1:6443"))
	sw := switchesOn(permissions.AWS)
	d, err := newDoor(nil, []CloudMount{aws}, sw.On)
	require.NoError(t, err)
	_, want := get(t, d, http.MethodGet, "unknown.kstack.invalid", "")

	sw.set(permissions.AWS, false)
	code, body := get(t, d, http.MethodGet, "127.0.0.1:6443", aws.Grant.Token())
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, want, body)

	sw.set(permissions.AWS, true)
	_, body = get(t, d, http.MethodGet, "127.0.0.1:6443", aws.Grant.Token())
	assert.Equal(t, "read aws", body)
}

func TestTwoMountsCannotNameOneHost(t *testing.T) {
	aws := mountFor(t, newFakeProvider(permissions.AWS, "127.0.0.1:6443"))
	clash := mountFor(t, newFakeProvider(permissions.Azure, "127.0.0.1:6443"))
	_, err := newDoor(nil, []CloudMount{aws, clash}, switchesOn().On)
	assert.ErrorContains(t, err, "127.0.0.1:6443")

	onCluster := mountFor(t, newFakeProvider(permissions.GCP, "cluster.kstack.invalid"))
	_, err = newDoor(http.NotFoundHandler(), []CloudMount{onCluster}, switchesOn().On)
	assert.ErrorContains(t, err, "cluster.kstack.invalid")
}
