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

package gcpproxy

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// classifyURL classifies method on a service's path and query.
func classifyURL(t *testing.T, service, method, target string) verdict {
	t.Helper()
	path, query, _ := strings.Cut(target, "?")
	segs, why := checkPath(serviceNamed(service), path)
	require.Empty(t, why, target)
	q, err := url.ParseQuery(query)
	require.NoError(t, err)
	return classify(service, method, segs, q)
}

func TestEveryRequestIsClassified(t *testing.T) {
	isRefused := verdict{kind: refused}
	isData := verdict{kind: asks, class: permissions.SecretRead}
	isRead := verdict{kind: read}
	isChange := verdict{kind: asks, class: permissions.UpstreamWrite}
	isDestructive := verdict{kind: asks, class: permissions.Destructive}
	cases := []struct {
		name, service, method, target string
		want                          verdict
	}{
		{"a key create", "iam", http.MethodPost, "/v1/projects/p/serviceAccounts/sa@p.iam.gserviceaccount.com/keys", isRefused},
		{"a signBlob", "iam", http.MethodPost, "/v1/projects/-/serviceAccounts/sa:signBlob", isRefused},
		{"a signJwt", "iam", http.MethodPost, "/v1/projects/-/serviceAccounts/sa:signJwt", isRefused},
		{"an access token", "iam", http.MethodPost, "/v1/projects/-/serviceAccounts/sa:generateAccessToken", isRefused},
		{"an ID token", "iam", http.MethodPost, "/v1/projects/-/serviceAccounts/sa:generateIdToken", isRefused},
		{"an HMAC key create", "storage", http.MethodPost, "/storage/v1/projects/p/hmacKeys?serviceAccountEmail=sa", isRefused},
		{"an OAuth client credential's create", "iam", http.MethodPost, "/v1/projects/p/locations/global/oauthClients/c/credentials", isRefused},
		{"an OAuth client credential's get", "iam", http.MethodGet, "/v1/projects/p/locations/global/oauthClients/c/credentials/k", isRefused},
		{"an OAuth client credentials' list", "iam", http.MethodGet, "/v1/projects/p/locations/global/oauthClients/c/credentials", isRefused},
		{"a SCIM token's create", "iam", http.MethodPost, "/v1/locations/global/workforcePools/w/providers/p/scimTenants/s/tokens", isRefused},
		{"a SCIM token's get", "iam", http.MethodGet, "/v1/locations/global/workforcePools/w/providers/p/scimTenants/s/tokens/t", isRefused},
		{"a resumable upload", "storage", http.MethodPost, "/upload/storage/v1/b/bkt/o?uploadType=resumable&name=x", isRefused},
		{"alt=media", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?alt=media", isData},
		{"$alt=media", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?$alt=media", isData},
		{"an alt sent twice, the second media", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?alt=json&alt=media", isData},
		{"ALT=media", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?ALT=media", isData},
		{"$Alt=MEDIA", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?$Alt=MEDIA", isData},
		{"$uploadType=resumable", "storage", http.MethodPost, "/upload/storage/v1/b/bkt/o?$uploadType=resumable", isRefused},
		{"uploadType=Resumable", "storage", http.MethodPost, "/upload/storage/v1/b/bkt/o?uploadType=Resumable", isRefused},
		{"upload_protocol=resumable", "storage", http.MethodPost, "/upload/storage/v1/b/bkt/o?upload_protocol=resumable", isRefused},
		{"an HMAC key create under /download/", "storage", http.MethodPost, "/download/storage/v1/projects/p/hmacKeys", isRefused},
		{"an HMAC key create under /upload/", "storage", http.MethodPost, "/upload/storage/v1/projects/p/hmacKeys", isRefused},
		{"a download path", "storage", http.MethodGet, "/download/storage/v1/b/bkt/o/x", isData},
		{"a serial port", "compute", http.MethodGet, "/compute/v1/projects/p/zones/z/instances/i/serialPort?port=1", isData},
		{"guest attributes", "compute", http.MethodGet, "/compute/v1/projects/p/zones/z/instances/i/getGuestAttributes", isData},
		{"a screenshot", "compute", http.MethodGet, "/compute/beta/projects/p/zones/z/instances/i/screenshot", isData},
		{"an instance list", "compute", http.MethodGet, "/compute/v1/projects/p/zones/z/instances", isRead},
		{"an object's metadata", "storage", http.MethodGet, "/storage/v1/b/bkt/o/x?alt=json", isRead},
		{"a HEAD", "storage", http.MethodHead, "/storage/v1/b/bkt", isRead},
		{"a key list", "iam", http.MethodGet, "/v1/projects/p/serviceAccounts/sa/keys", isRead},
		{"compute's getIamPolicy", "compute", http.MethodGet, "/compute/v1/projects/p/zones/z/instances/i/getIamPolicy", isRead},
		{"compute's testIamPermissions", "compute", http.MethodPost, "/compute/v1/projects/p/zones/z/instances/i/testIamPermissions", isRead},
		{"Resource Manager's getIamPolicy", "cloudresourcemanager", http.MethodPost, "/v1/projects/p:getIamPolicy", isRead},
		{"Resource Manager's testIamPermissions", "cloudresourcemanager", http.MethodPost, "/v3/projects/p:testIamPermissions", isRead},
		{"entries:list", "logging", http.MethodPost, "/v2/entries:list", isRead},
		{"a project's delete", "cloudresourcemanager", http.MethodDelete, "/v3/projects/p", isDestructive},
		{"a cluster's delete", "container", http.MethodDelete, "/v1/projects/p/locations/us-central1/clusters/dev", isDestructive},
		{"a zonal cluster's delete", "container", http.MethodDelete, "/v1/projects/p/zones/us-central1-a/clusters/dev", isDestructive},
		{"a bucket's delete", "storage", http.MethodDelete, "/storage/v1/b/bkt", isDestructive},
		{"a service account's delete", "iam", http.MethodDelete, "/v1/projects/p/serviceAccounts/sa@p.iam.gserviceaccount.com", isDestructive},
		{"a setIamPolicy", "compute", http.MethodPost, "/compute/v1/projects/p/zones/z/instances/i/setIamPolicy", isDestructive},
		{"a :setIamPolicy", "cloudresourcemanager", http.MethodPost, "/v1/projects/p:setIamPolicy", isDestructive},
		{"a bucket's IAM PUT", "storage", http.MethodPut, "/storage/v1/b/bkt/iam", isDestructive},
		{"a managed folder's IAM PUT", "storage", http.MethodPut, "/storage/v1/b/bkt/managedFolders/f/iam", isDestructive},
		{"a bucket's delete under /upload/", "storage", http.MethodDelete, "/upload/storage/v1/b/bkt", isDestructive},
		{"an object named iam's update", "storage", http.MethodPut, "/storage/v1/b/bkt/o/iam", isChange},
		{"an instance's delete", "compute", http.MethodDelete, "/compute/v1/projects/p/zones/z/instances/i", isChange},
		{"an object's delete", "storage", http.MethodDelete, "/storage/v1/b/bkt/o/x", isChange},
		{"a node pool's delete", "container", http.MethodDelete, "/v1/projects/p/locations/l/clusters/c/nodePools/np", isChange},
		{"a log write", "logging", http.MethodPost, "/v2/entries:write", isChange},
		{"a media upload", "storage", http.MethodPost, "/upload/storage/v1/b/bkt/o?uploadType=media&name=x", isChange},
		{"a POST with alt=media", "storage", http.MethodPost, "/storage/v1/b/bkt/o/x/compose?alt=media", isChange},
	}
	for _, tc := range cases {
		got := classifyURL(t, tc.service, tc.method, tc.target)
		assert.Equal(t, tc.want.kind, got.kind, tc.name)
		assert.Equal(t, tc.want.class, got.class, tc.name)
		if tc.want.kind == refused {
			assert.True(t, strings.HasPrefix(got.why, "kstack: "), tc.name)
		}
	}
}
