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

package kubeproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"sigs.k8s.io/yaml"

	"github.com/kstackhq/kstack/sidecar/internal/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/safe"
)

const (
	// maxQueuedWrites is how many writes may wait for the one ahead of them.
	maxQueuedWrites = 8
	// maxWriteBody is the largest change a user can be asked to read, below the
	// API server's own 3 MiB.
	maxWriteBody = 1 << 20
)

// showableTypes are the media types of a body the user reads as sent.
var showableTypes = map[string]bool{
	"application/json":                       true,
	"application/merge-patch+json":           true,
	"application/strategic-merge-patch+json": true,
	"application/json-patch+json":            true,
	"application/apply-patch+yaml":           true,
}

// redactedMarks are what the sandbox reads in place of a redacted value: the
// text, and its base64 as a Secret's data holds it.
var redactedMarks = []string{safe.Redacted, redactedData}

// releasePrefix names every helm release Secret.
const releasePrefix = "sh.helm.release.v1."

// Write is one held request, as the user will see it.
type Write struct {
	Method      string
	Path        string // path and raw query, as forwarded
	Subresource string // the path's subresource as the policy parsed it; "" for none
	ContentType string
	Body        []byte // valid UTF-8, or empty
	DryRun      bool   // a POST, PUT or PATCH whose every dryRun is All
}

// Request is one classified action held for the user, or decided with nobody
// asked.
type Request struct {
	Action permissions.Action
	// Write is the request as sent; nil for an action with none.
	Write *Write
	// Diff is the change as a unified diff of YAML; "" for none.
	Diff string
	// DiffCut is a diff that stops short of the whole change.
	DiffCut bool
	// DiffError is why there is no diff, when one was looked for.
	DiffError string
}

// Answer is the user's decision.
type Answer struct {
	Approved bool
}

// Asker puts a request to the user, and records one decided with nobody
// asked. From Ask, a context error is a wait that ended without a decision,
// which the asker records as abandoned; any other error is a request or
// decision the asker could not record. From Record, an error is a record the
// asker could not write; reason is the decision in the user's words.
type Asker interface {
	Ask(ctx context.Context, r Request) (Answer, error)
	Record(ctx context.Context, r Request, d permissions.Decision, reason string) error
}

// serveWrite decides a write the policy passed: forwards it when the policy
// allows it, refuses it when the policy denies it, and otherwise puts it to
// the user and forwards it once approved. Each one decided with nobody asked
// is recorded first.
func (g *Grant) serveWrite(w http.ResponseWriter, r *http.Request, p apiPath) {
	// What needs no body is refused before the queue, so a burst of them reads
	// its own refusal.
	if g.asker == nil {
		writeStatus(w, http.StatusForbidden, g.refusal)
		return
	}
	if r.Method != http.MethodPost && namesRelease(p) {
		writeStatus(w, http.StatusForbidden, string(refusedHelm))
		return
	}
	// A pair that does not parse is dropped on its way to the API server, so the
	// query shown would not be the query that runs: a selector could vanish.
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		writeStatus(w, http.StatusForbidden, string(refusedQuery))
		return
	}
	// Held until the forward returns, so the cluster sees writes in the order
	// they were decided, and one wait on the user at a time.
	if !g.takeWriteLock(r.Context(), w) {
		return
	}
	defer g.writeLock.Release(1)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriteBody))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeStatus(w, http.StatusForbidden, string(refusedTooLarge))
		return
	}
	if err != nil {
		return
	}
	if why := checkBody(r, p, body); why != pass {
		writeStatus(w, http.StatusForbidden, string(why))
		return
	}
	act := classify(r, p, body, g.context)
	write := Write{
		Method: r.Method, Path: r.URL.RequestURI(), Subresource: p.subresource,
		ContentType: r.Header.Get("Content-Type"), Body: body, DryRun: act.DryRun,
	}
	req := Request{Action: act, Write: &write}
	switch d, reason := g.policy(r.Context()).Decide(act); d {
	case permissions.Allowed:
		if err := g.asker.Record(r.Context(), req, d, reason); err != nil {
			writeStatus(w, http.StatusForbidden, string(refusedUnrecorded))
			return
		}
		g.forward(w, r, p, body)
		return
	case permissions.Denied:
		// Nothing runs either way, so the refusal does not wait on the record.
		if err := g.asker.Record(r.Context(), req, d, reason); err != nil {
			slog.Warn("a refused cluster write was not recorded", "err", err)
		}
		writeStatus(w, http.StatusForbidden, "kstack: "+act.Summary+" is not allowed: "+reason)
		return
	}
	pv := g.previewChange(r, p, act, body)
	req.Diff, req.DiffCut, req.DiffError = pv.diff, pv.cut, pv.err
	answer, err := g.asker.Ask(r.Context(), req)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeStatus(w, http.StatusForbidden, string(refusedUnanswered))
		return
	}
	if err != nil {
		writeStatus(w, http.StatusForbidden, string(refusedUnrecorded))
		return
	}
	if !answer.Approved {
		writeStatus(w, http.StatusForbidden, string(refusedDenied))
		return
	}
	g.forward(w, r, p, body)
}

// policy is the session's mode and rules for the grant's context, read now, so
// a change made meanwhile applies. A session with no policy is read-only.
func (g *Grant) policy(ctx context.Context) permissions.Policy {
	if g.session.Policy == nil {
		return permissions.Policy{Mode: permissions.ReadOnly}
	}
	return g.session.Policy(ctx, g.context)
}

// takeWriteLock waits for the write lock, as one of at most maxQueuedWrites, and
// answers false, having answered w, when the queue is full or ctx ends first.
// The body is read only once the lock is held, so a queued write holds nothing
// but its connection.
func (g *Grant) takeWriteLock(ctx context.Context, w http.ResponseWriter) bool {
	if !g.writeWaiters.TryAcquire(1) {
		// No Retry-After, so client-go does not retry it.
		writeStatus(w, http.StatusTooManyRequests, "kstack: too many changes are waiting on the user. Send one at a time.")
		return false
	}
	defer g.writeWaiters.Release(1)
	g.waiting(1)
	defer g.waiting(-1)
	return g.writeLock.Acquire(ctx, 1) == nil
}

// waiting tells waitersMoved, when set, that delta writes started or stopped
// waiting for the lock.
func (g *Grant) waiting(delta int) {
	if g.waitersMoved != nil {
		g.waitersMoved(delta)
	}
}

// checkBody is why a write's body cannot be put to the user, or pass. A DELETE
// may carry none, and then has no type to check.
func checkBody(r *http.Request, p apiPath, body []byte) refusal {
	if r.Header.Get("Content-Encoding") != "" || !utf8.Valid(body) {
		return refusedUnshowable
	}
	if len(body) == 0 && r.Method == http.MethodDelete {
		return pass
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !showableTypes[mediaType] {
		return refusedUnshowable
	}
	if len(body) > 0 {
		value, err := decodeBody(mediaType, body)
		if err != nil {
			return refusedUnshowable
		}
		// decodeBody keeps a repeated key's last value, where the API server's
		// typed decoder merges repeated objects, so a body repeating one would
		// be classified and shown as other than it runs.
		if !uniqueKeys(mediaType, body) {
			return refusedRepeatedKey
		}
		if holdsMark(value) {
			return refusedRedacted
		}
	}
	if r.Method == http.MethodPost && p.onSecrets() && !notARelease(body) {
		return refusedHelm
	}
	return pass
}

// decodeBody is body as the API server decodes it for its media type: an apply
// patch through sigs.k8s.io/yaml, as the server reads one, anything else as one
// JSON value. An escape is decoded, so a mark it spells differently is found.
func decodeBody(mediaType string, body []byte) (any, error) {
	if mediaType == "application/apply-patch+yaml" {
		converted, err := yaml.YAMLToJSON(body)
		if err != nil {
			return nil, err
		}
		body = converted
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("kubeproxy: a body holds more than one value")
	}
	return value, nil
}

// uniqueKeys is whether body parses with no object repeating a key.
func uniqueKeys(mediaType string, body []byte) bool {
	if mediaType == "application/apply-patch+yaml" {
		_, err := yaml.YAMLToJSONStrict(body)
		return err == nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	return uniqueKeysIn(dec) && !dec.More()
}

// uniqueKeysIn reads the next JSON value off dec, false if it does not parse or
// an object in it repeats a key.
func uniqueKeysIn(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil || seen[key.(string)] {
				return false
			}
			seen[key.(string)] = true
			if !uniqueKeysIn(dec) {
				return false
			}
		}
	case json.Delim('['):
		for dec.More() {
			if !uniqueKeysIn(dec) {
				return false
			}
		}
	default:
		return true
	}
	_, err = dec.Token()
	return err == nil
}

// holdsMark is whether any string in value, a key included, holds a redacted
// mark.
func holdsMark(value any) bool {
	switch v := value.(type) {
	case string:
		for _, mark := range redactedMarks {
			if strings.Contains(v, mark) {
				return true
			}
		}
	case map[string]any:
		for k, item := range v {
			if holdsMark(k) || holdsMark(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if holdsMark(item) {
				return true
			}
		}
	}
	return false
}

// namesRelease is whether p names a helm release Secret.
func namesRelease(p apiPath) bool {
	return p.onSecrets() && strings.HasPrefix(p.name, releasePrefix)
}

// notARelease is whether body is a JSON object whose type is not a helm
// release's. The keys are matched exactly, as the API server matches them.
func notARelease(body []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return false
	}
	var typ string
	if raw, ok := obj["type"]; ok && json.Unmarshal(raw, &typ) != nil {
		return false
	}
	return typ != helmReleaseType
}

// isDryRun is whether r asks the API server for a dry run, read strictly since
// the request claims it: a POST, PUT or PATCH whose query names dryRun, and
// every value All. The API server reads a DELETE's options from its body when
// it has one, and ignores the query then, so a DELETE is never one.
func isDryRun(r *http.Request) bool {
	if r.Method == http.MethodDelete {
		return false
	}
	values := r.URL.Query()["dryRun"]
	for _, v := range values {
		if v != "All" {
			return false
		}
	}
	return len(values) > 0
}
