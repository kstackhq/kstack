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

package awsproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
)

// operation is what the classifier read of a request.
type operation struct {
	name    string            // a query or JSON operation; "" for a REST row
	class   permissions.Class // 0 for a read
	refusal string            // why it is not sent, "" to send it
	bucket  string            // an S3 request's bucket, "" for none
}

// s3MetadataQuery are the subresources of an S3 key whose GET answers
// metadata, not the object's data.
var s3MetadataQuery = []string{"acl", "attributes", "legal-hold", "retention", "tagging", "uploadId"}

// classify reads one request to the row service names: refused, then class
// 6, class 5, a read, and class 4 for the rest, so what it cannot name asks.
// A REST row classifies from the method, path and query alone.
func classify(service string, r *http.Request, body []byte) operation {
	row := services[service]
	if row.protocol == protocolREST {
		return classifyREST(service, r)
	}
	op := operation{class: permissions.UpstreamWrite}
	// The operation is read only where the row's protocol names it, so the one
	// classified is the one AWS runs.
	target := r.Header.Get("X-Amz-Target")
	params := queryParams(r, body)
	actions, hasAction := params["Action"]
	if target != "" && (hasAction || row.protocol == protocolQuery) || hasAction && row.protocol == protocolJSON ||
		len(actions) > 1 {
		op.refusal = "kstack: this request names its operation where " + service +
			" does not read it, or twice, so the sandbox does not send it"
		return op
	}
	if target != "" {
		_, op.name, _ = strings.Cut(target, ".")
	} else if len(actions) == 1 {
		op.name = actions[0]
	}
	if op.name == "" {
		return op
	}
	switch {
	case listed(row.refused, op.name) || service == "sts" && op.name != "GetCallerIdentity":
		op.refusal = credentialRefusal(service + ":" + op.name)
	case listed(row.secrets, op.name) || secretRead(service, op.name, params, body):
		op.class = permissions.SecretRead
	case hasAnyPrefix(op.name, "Delete", "Terminate") || listed(row.destroys, op.name):
		op.class = permissions.Destructive
	case foreign(service, op.name, params, body):
		// Class 4 though it reads: another account's log would record it.
	case hasAnyPrefix(op.name, "Describe", "Get", "List") || listed(row.reads, op.name):
		op.class = 0
	}
	return op
}

// classifyREST reads a REST row's request: a GET or HEAD reads, a DELETE is
// class 5, and S3 has its own subresources.
func classifyREST(service string, r *http.Request) operation {
	op := operation{class: permissions.UpstreamWrite}
	method, query := r.Method, r.URL.Query()
	s3 := service == "s3"
	var key string
	if s3 {
		op.bucket, key, _ = strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	}
	switch {
	case s3 && query.Has("session"):
		op.refusal = credentialRefusal("S3's CreateSession")
	case s3 && method == http.MethodGet && key != "" && !slices.ContainsFunc(s3MetadataQuery, query.Has),
		s3 && method == http.MethodPost && query.Has("select"):
		op.class = permissions.SecretRead
	case method == http.MethodDelete, s3 && method == http.MethodPost && query.Has("delete"):
		op.class = permissions.Destructive
	case method == http.MethodGet || method == http.MethodHead:
		op.class = 0
	}
	return op
}

// credentialRefusal is the answer to an operation whose answer is a
// credential.
func credentialRefusal(what string) string {
	return "kstack: " + what + " answers a credential, so the sandbox does not send it"
}

// unmaskable are the log reads that take an unmask.
var unmaskable = []string{"GetLogEvents", "FilterLogEvents", "GetLogRecord", "StartQuery"}

// secretRead is a class 6 rule that reads the request's parameters: EC2's
// attribute read that can answer user data, and a log read that unmasks.
func secretRead(service, op string, params url.Values, body []byte) bool {
	switch {
	case service == "ec2" && op == "DescribeInstanceAttribute":
		attrs := params["Attribute"]
		return len(attrs) != 1 || attrs[0] == "userData"
	case service == "logs" && slices.Contains(unmaskable, op):
		return mentions(body, "unmask")
	}
	return false
}

// foreign is a read that names a resource another account may own, whose log
// would then record it: an ECR registry, or a template AWS fetches from a URL.
func foreign(service, op string, params url.Values, body []byte) bool {
	switch {
	case service == "ecr":
		return mentions(body, "registryid")
	case service == "cloudformation" && (op == "ValidateTemplate" || op == "EstimateTemplateCost"):
		return params.Has("TemplateURL")
	}
	return false
}

// mentions is whether any key or string in the JSON body holds word, in any
// case. Every token is read, so a duplicate key or an escape cannot hide one;
// a body that is not JSON mentions everything.
func mentions(body []byte, word string) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if s, ok := tok.(string); ok && strings.Contains(strings.ToLower(s), word) {
			return true
		}
	}
}

// queryParams are a query row's parameters: the URL's, and a form body's.
// A form that does not parse names no operation, so it asks.
func queryParams(r *http.Request, body []byte) url.Values {
	params := r.URL.Query()
	if runproxy.IsForm(r.Header.Get("Content-Type")) {
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return url.Values{}
		}
		for k, v := range form {
			params[k] = append(params[k], v...)
		}
	}
	return params
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(s, p) })
}
