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
	"cmp"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
)

// kind is what the proxy does with a request.
type kind int

const (
	read    kind = iota // forwarded unasked
	asks                // decided by the grant
	refused             // never sent
)

// verdict is a request's kind and access level, the action decided when it
// is decided, and why it is refused.
type verdict struct {
	kind  kind
	level permissions.AccessLevel
	act   permissions.Action
	why   string
}

const (
	refusedCredential = "kstack: this request's answer is a credential, so it was not sent."
	refusedResumable  = "kstack: a resumable upload answers a session that uploads with no token, so it was not sent."
)

// classify reads a request on the service named svc, its path's decoded
// segments, its query and its body: the first rule that applies decides. A
// request the grant decides carries its action; a read carries none, since
// it skips the decision.
func classify(svc, method string, segs []string, q url.Values, body []byte) verdict {
	last := segs[len(segs)-1]
	res := resource(svc, segs)
	var l permissions.AccessLevel
	switch {
	case answersACredential(svc, method, res):
		return verdict{kind: refused, level: permissions.Reach, why: refusedCredential}
	case svc == "storage" && hasSystemValue(q, "resumable", "uploadtype", "upload_protocol"):
		return verdict{kind: refused, why: refusedResumable}
	case method == http.MethodGet && readsData(svc, segs, q):
		l = permissions.DataRead
	case method == http.MethodGet || method == http.MethodHead:
		return verdict{kind: read, level: permissions.Read}
	case method == http.MethodPost && (isMethod(last, "getIamPolicy") || isMethod(last, "testIamPermissions")):
		return verdict{kind: read, level: permissions.Read}
	case method == http.MethodPost && svc == "logging" && slices.Equal(segs, []string{"v2", "entries:list"}):
		return verdict{kind: read, level: permissions.Read}
	case changesAccessControl(svc, method, res):
		l = permissions.AccessControl
	case isDestructive(svc, method, res):
		l = permissions.Destructive
	default:
		l = permissions.Write
	}
	return verdict{kind: asks, level: l, act: action(svc, method, segs, body, l)}
}

// ordinary are the changes a mode or a rule may allow, by service, then
// resource, then operation. Any other write asks each time. Adding a row is
// a security change.
var ordinary = map[string]map[string][]string{
	"compute": {"instances": {"start", "reset", "setlabels"}},
}

// action is the action a request the grant decides carries. Its scope is
// the project named as an id, then the location, both in lower case; a
// project named any other way, a path that names none, and a body that names
// another project or is not JSON give no scope.
func action(svc, method string, segs []string, body []byte, l permissions.AccessLevel) permissions.Action {
	p := parsePath(svc, segs)
	act := permissions.Action{
		Service: svc, Operation: strings.ToLower(cmp.Or(p.method, method)), Resource: strings.Join(p.types, "/"),
		Name: p.name, AccessLevel: l, AccountName: p.project, Summary: summary(svc, method, segs),
	}
	if projectID.MatchString(p.project) && !foreign(body, p.project) {
		act.Scope = p.project
		if p.location != "" {
			act.Scope += "/" + p.location
		}
	}
	if l == permissions.Write {
		act.Once = method != http.MethodPost || !slices.Contains(ordinary[svc][act.Resource], act.Operation)
	}
	return act
}

// projectID is a project named as an id: a project number and "-" are not
// one.
var projectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// locationWords are the collections a location is named under.
var locationWords = []string{"zones", "regions", "locations"}

// apiPath is what action reads of a path past its API's version.
type apiPath struct {
	project, location string
	types             []string // each collection's type, in lower case
	name              string   // the last collection member named
	method            string   // the custom method, "" for none
}

// parsePath reads segs as collection and member pairs past the version: the
// first project, the location right after it, each other collection's type,
// and a custom method, after ":" or, on compute, a segment of its own after a
// member.
func parsePath(svc string, segs []string) apiPath {
	rest := pastVersion(segs)
	var p apiPath
	afterMember := false
	for i := 0; i < len(rest); i += 2 {
		coll := rest[i]
		if svc == "compute" && coll == "global" {
			i--
			continue
		}
		if i+1 == len(rest) {
			coll, m, ok := strings.Cut(coll, ":")
			switch {
			case ok:
				p.method = m
				p.types = append(p.types, strings.ToLower(coll))
			case svc == "compute" && afterMember:
				p.method = coll
			default:
				p.types = append(p.types, strings.ToLower(coll))
			}
			break
		}
		member, m, ok := strings.Cut(rest[i+1], ":")
		if ok {
			p.method = m
		}
		switch {
		case coll == "projects" && p.project == "":
			p.project = strings.ToLower(member)
			afterMember = false
			continue
		case slices.Contains(locationWords, coll) && p.location == "" && p.project != "" && len(p.types) == 0:
			p.location = strings.ToLower(member)
			afterMember = false
			continue
		}
		p.types = append(p.types, strings.ToLower(coll))
		p.name, afterMember = member, true
	}
	if len(p.types) == 0 && p.project != "" {
		p.types, p.name = []string{"projects"}, p.project
	}
	return p
}

// foreign is whether body may name a project other than project: a body
// that is not JSON, or one whose keys or strings, read token by token, hold
// projects/ followed by another name. An empty body names nothing.
func foreign(body []byte, project string) bool {
	if len(body) == 0 {
		return false
	}
	return runproxy.AnyJSONString(body, func(s string) bool { return namesAnotherProject(s, project) })
}

// namesAnotherProject is whether s holds projects/ followed by a name other
// than project.
func namesAnotherProject(s, project string) bool {
	for {
		i := strings.Index(strings.ToLower(s), "projects/")
		if i < 0 {
			return false
		}
		s = s[i+len("projects/"):]
		end := strings.IndexFunc(s, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		})
		if end < 0 {
			end = len(s)
		}
		if strings.ToLower(s[:end]) != project {
			return true
		}
	}
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

// changesAccessControl is whether a change is to who may do what: a
// setIamPolicy, a storage IAM PUT, a bucket's or a managed folder's, and
// every change on the iam service.
func changesAccessControl(svc, method string, segs []string) bool {
	n := len(segs)
	switch {
	case svc == "iam":
		return true
	case method == http.MethodPost:
		return isMethod(segs[n-1], "setIamPolicy")
	case method == http.MethodPut:
		return svc == "storage" && segs[n-1] == "iam" && segs[n-2] != "o"
	}
	return false
}

// isDestructive is whether a change is on the destructive list: a DELETE of a
// project, a GKE cluster or a bucket.
func isDestructive(svc, method string, segs []string) bool {
	n := len(segs)
	switch method {
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
		}
	}
	return false
}
