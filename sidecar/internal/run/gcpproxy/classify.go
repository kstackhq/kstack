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
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// kind is what the proxy does with a request.
type kind int

const (
	read    kind = iota // forwarded unasked
	asks                // put to the user, at its class
	refused             // never sent
)

// verdict is a request's kind, its class when it asks, and why it is
// refused.
type verdict struct {
	kind  kind
	class permissions.Class
	why   string
}

const (
	refusedCredential = "kstack: this request's answer is a credential, so it was not sent."
	refusedResumable  = "kstack: a resumable upload answers a session that uploads with no token, so it was not sent."
)

// classify reads a request on the service named svc, its path's decoded
// segments and its query: the first rule that applies decides.
func classify(svc, method string, segs []string, q url.Values) verdict {
	last := segs[len(segs)-1]
	res := resource(svc, segs)
	switch {
	case answersACredential(svc, method, res):
		return verdict{kind: refused, why: refusedCredential}
	case svc == "storage" && hasSystemValue(q, "resumable", "uploadtype", "upload_protocol"):
		return verdict{kind: refused, why: refusedResumable}
	case method == http.MethodGet && readsData(svc, segs, q):
		return verdict{kind: asks, class: permissions.SecretRead}
	case method == http.MethodGet || method == http.MethodHead:
		return verdict{kind: read}
	case method == http.MethodPost && (isMethod(last, "getIamPolicy") || isMethod(last, "testIamPermissions")):
		return verdict{kind: read}
	case method == http.MethodPost && svc == "logging" && slices.Equal(segs, []string{"v2", "entries:list"}):
		return verdict{kind: read}
	case isDestructive(svc, method, res):
		return verdict{kind: asks, class: permissions.Destructive}
	}
	return verdict{kind: asks, class: permissions.UpstreamWrite}
}

// resource is segs past storage's upload or download prefix, so a rule that
// reads a segment by its place reads it under every prefix.
func resource(svc string, segs []string) []string {
	if svc == "storage" && (segs[0] == "upload" || segs[0] == "download") {
		return segs[1:]
	}
	return segs
}

// isMethod is whether seg calls name: the whole segment, as compute spells a
// custom method, or its suffix after ":", as the other APIs do.
func isMethod(seg, name string) bool {
	return seg == name || strings.HasSuffix(seg, ":"+name)
}

// answersACredential is whether the request's answer is a credential: a
// service account key or a token or signature made with one, an HMAC key, an
// OAuth client's secret or a SCIM token.
func answersACredential(svc, method string, segs []string) bool {
	last := segs[len(segs)-1]
	for _, m := range []string{"signBlob", "signJwt", "generateAccessToken", "generateIdToken"} {
		if strings.HasSuffix(last, ":"+m) {
			return true
		}
	}
	switch svc {
	case "iam":
		return method == http.MethodPost && last == "keys" ||
			under(segs, "oauthClients", "credentials") || under(segs, "scimTenants", "tokens")
	case "storage":
		return method == http.MethodPost && len(segs) >= 5 && segs[2] == "projects" && segs[4] == "hmacKeys"
	}
	return false
}

// under is whether segs holds collection somewhere below parent.
func under(segs []string, parent, collection string) bool {
	i := slices.Index(segs, parent)
	return i >= 0 && slices.Contains(segs[i+1:], collection)
}

// readsData is whether a GET answers stored data or what a guest wrote: an
// object's contents, a serial port, guest attributes or a screenshot.
func readsData(svc string, segs []string, q url.Values) bool {
	if hasSystemValue(q, "media", "alt") {
		return true
	}
	switch svc {
	case "storage":
		return segs[0] == "download"
	case "compute":
		return slices.Contains([]string{"serialPort", "getGuestAttributes", "screenshot"}, segs[len(segs)-1])
	}
	return false
}

// isDestructive is whether a change is on the destructive list: a DELETE of a
// project, a GKE cluster, a bucket or a service account, a setIamPolicy, and
// a storage IAM PUT, a bucket's or a managed folder's.
func isDestructive(svc, method string, segs []string) bool {
	n := len(segs)
	switch method {
	case http.MethodPost:
		return isMethod(segs[n-1], "setIamPolicy")
	case http.MethodPut:
		return svc == "storage" && segs[n-1] == "iam" && segs[n-2] != "o"
	case http.MethodDelete:
		parent := ""
		if n >= 2 {
			parent = segs[n-2]
		}
		switch svc {
		case "cloudresourcemanager":
			return n == 3 && parent == "projects"
		case "container":
			return parent == "clusters"
		case "storage":
			return n == 4 && parent == "b"
		case "iam":
			return parent == "serviceAccounts"
		}
	}
	return false
}
