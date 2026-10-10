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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// input is one call's arguments as the model typed them, each checked for shape.
type input struct {
	description string
	sources     []source
	filters     []tools.LogsViewFilter
	grep        string
	anchor      string
	pinToEnd    bool
}

// source is one source as typed: its resource read into a kind and a name.
type source struct {
	namespace     string
	kind          tools.LogsSourceKind
	name          string
	containers    []string
	allContainers bool
	previous      bool
}

// inputError is arguments the tool refuses. field names the argument when it is
// one the tool knows, "" otherwise; source is the source it concerns, -1 for
// none; message, the tool's own words, says what to change.
type inputError struct {
	field, message string
	source         int
}

func (e *inputError) Error() string {
	if e.field == "" {
		return "logsview input is not one object of the tool's own fields"
	}
	return "logsview input has a bad " + e.field
}

// maxGrep bounds a grep, so the receipt that repeats it stays one line.
const maxGrep = 1_000

// anchorMessage is the refusal of an anchor the tool cannot read.
const anchorMessage = "tail, head, an RFC 3339 time, or a duration back from now such as 2m"

// kindOf is each resource as kubectl spells it, plural, singular and short.
var kindOf = map[string]tools.LogsSourceKind{
	"pods": tools.LogsSourcePod, "pod": tools.LogsSourcePod, "po": tools.LogsSourcePod,
	"deployments": tools.LogsSourceDeployment, "deployment": tools.LogsSourceDeployment, "deploy": tools.LogsSourceDeployment,
	"statefulsets": tools.LogsSourceStatefulSet, "statefulset": tools.LogsSourceStatefulSet, "sts": tools.LogsSourceStatefulSet,
	"daemonsets": tools.LogsSourceDaemonSet, "daemonset": tools.LogsSourceDaemonSet, "ds": tools.LogsSourceDaemonSet,
	"jobs": tools.LogsSourceJob, "job": tools.LogsSourceJob,
	"cronjobs": tools.LogsSourceCronJob, "cronjob": tools.LogsSourceCronJob, "cj": tools.LogsSourceCronJob,
	"replicasets": tools.LogsSourceReplicaSet, "replicaset": tools.LogsSourceReplicaSet, "rs": tools.LogsSourceReplicaSet,
}

// filterFields is each filter key and its field, in the order the action lists them.
var filterFields = []struct {
	key   string
	field tools.LogsFilterField
}{
	{"node", tools.LogsFilterNode}, {"region", tools.LogsFilterRegion}, {"zone", tools.LogsFilterZone},
	{"os", tools.LogsFilterOS}, {"arch", tools.LogsFilterArch},
}

// parse reads an object whose keys are the tool's fields, each spelled exactly and
// at most once, with nothing after it, and checks each value off its token, since
// a typed decode takes null as the zero value.
func parse(raw json.RawMessage) (input, error) {
	in := input{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	w := walker{dec}
	err := w.object(func(key string) error {
		switch key {
		case "description":
			return w.string(&in.description, key)
		case "sources":
			return w.array(func(i int) error {
				s, err := parseSource(w, i)
				in.sources = append(in.sources, s)
				return err
			})
		case "filters":
			return parseFilters(w, &in.filters)
		case "grep":
			return w.string(&in.grep, key)
		case "anchor":
			return w.string(&in.anchor, key)
		case "pin_to_end":
			return w.bool(&in.pinToEnd, key)
		}
		return &inputError{source: -1}
	})
	if err != nil {
		return input{}, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input{}, &inputError{source: -1}
	}

	if len(in.sources) == 0 {
		return input{}, &inputError{field: "sources", source: -1}
	}
	if len(in.grep) > maxGrep || strings.ContainsAny(in.grep, "\r\n") {
		return input{}, &inputError{field: "grep", source: -1}
	}
	if _, err := regexp.Compile(in.grep); err != nil {
		return input{}, &inputError{field: "grep", source: -1, message: "does not compile as a regular expression"}
	}
	return in, nil
}

// parseSource reads source i: its resource as <kind>/<name>, the namespace and
// each container a DNS name, and all_containers refused beside containers.
func parseSource(w walker, i int) (source, error) {
	s := source{}
	var resource string
	err := w.object(func(key string) error {
		switch key {
		case "namespace":
			return w.string(&s.namespace, key)
		case "resource":
			return w.string(&resource, key)
		case "containers":
			return w.strings(&s.containers, key)
		case "all_containers":
			return w.bool(&s.allContainers, key)
		case "previous":
			return w.bool(&s.previous, key)
		}
		return &inputError{source: i}
	})
	if err != nil {
		var ie *inputError
		if errors.As(err, &ie) {
			ie.source = i
		}
		return source{}, err
	}
	if !dnsLabel(s.namespace) {
		return source{}, &inputError{field: "namespace", source: i}
	}
	kind, name, _ := strings.Cut(resource, "/")
	s.kind, s.name = kindOf[strings.ToLower(kind)], name
	if s.kind == "" || !dnsSubdomain(name) {
		return source{}, &inputError{field: "resource", source: i, message: "<kind>/<name>: pods, deployments, statefulsets, daemonsets, jobs, cronjobs or replicasets"}
	}
	for _, c := range s.containers {
		if !dnsLabel(c) {
			return source{}, &inputError{field: "containers", source: i}
		}
	}
	if s.allContainers && len(s.containers) > 0 {
		return source{}, &inputError{field: "all_containers", source: i, message: "not with containers"}
	}
	return s, nil
}

// parseFilters reads the filters object into one filter per field set, in the
// action's order, each a non-empty list of non-empty values.
func parseFilters(w walker, out *[]tools.LogsViewFilter) error {
	values := map[string][]string{}
	err := w.object(func(key string) error {
		for _, f := range filterFields {
			if f.key == key {
				var list []string
				if err := w.strings(&list, "filters."+key); err != nil {
					return err
				}
				values[key] = list
				return nil
			}
		}
		return &inputError{source: -1}
	})
	if err != nil {
		return err
	}
	for _, f := range filterFields {
		list, ok := values[f.key]
		if !ok {
			continue
		}
		if len(list) == 0 {
			return &inputError{field: "filters." + f.key, source: -1, message: "never empty"}
		}
		for _, v := range list {
			if v == "" {
				return &inputError{field: "filters." + f.key, source: -1, message: "never empty"}
			}
		}
		*out = append(*out, tools.LogsViewFilter{Field: f.field, Values: list})
	}
	return nil
}

// walker reads one JSON value token by token.
type walker struct{ dec *json.Decoder }

// object reads {, each key once and in turn through each, and }.
func (w walker) object(each func(key string) error) error {
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim('{') {
		return &inputError{source: -1}
	}
	seen := map[string]bool{}
	for w.dec.More() {
		tok, err := w.dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return &inputError{source: -1}
		}
		seen[key] = true
		if err := each(key); err != nil {
			return err
		}
	}
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim('}') {
		return &inputError{source: -1}
	}
	return nil
}

// array reads [, each element in turn through each with its index, and ].
func (w walker) array(each func(i int) error) error {
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim('[') {
		return &inputError{source: -1}
	}
	for i := 0; w.dec.More(); i++ {
		if err := each(i); err != nil {
			return err
		}
	}
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim(']') {
		return &inputError{source: -1}
	}
	return nil
}

// string reads a string into out, refusing any other token as a bad field.
func (w walker) string(out *string, field string) error {
	tok, err := w.dec.Token()
	s, ok := tok.(string)
	if err != nil || !ok {
		return &inputError{field: field, source: -1}
	}
	*out = s
	return nil
}

// bool reads a boolean into out, refusing any other token as a bad field.
func (w walker) bool(out *bool, field string) error {
	tok, err := w.dec.Token()
	b, ok := tok.(bool)
	if err != nil || !ok {
		return &inputError{field: field, source: -1}
	}
	*out = b
	return nil
}

// strings reads an array of strings into out, refusing anything else as a bad field.
func (w walker) strings(out *[]string, field string) error {
	tok, err := w.dec.Token()
	if err != nil || tok != json.Delim('[') {
		return &inputError{field: field, source: -1}
	}
	list := []string{}
	for w.dec.More() {
		var s string
		if err := w.string(&s, field); err != nil {
			return err
		}
		list = append(list, s)
	}
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim(']') {
		return &inputError{field: field, source: -1}
	}
	*out = list
	return nil
}

// dnsLabel is a name of at most 63 lower-case letters, digits and hyphens, as a
// namespace's and a container's are.
func dnsLabel(s string) bool { return dnsName(s, 63, false) }

// dnsSubdomain is a name of at most 253 lower-case letters, digits, hyphens and
// dots, as an object's is.
func dnsSubdomain(s string) bool { return dnsName(s, 253, true) }

func dnsName(s string, max int, dots bool) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, c := range s {
		switch {
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', dots && c == '.':
		default:
			return false
		}
	}
	return true
}
