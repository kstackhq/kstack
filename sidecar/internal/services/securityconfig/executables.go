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

package securityconfig

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Executable is a program the user asked the probe to check.
type Executable struct {
	Name       string `json:"name"`
	Invocation string `json:"invocation"` // "<name> --version" when the user gave none
}

// CuratedExecutables is what the probe checks on every machine, in this order.
// A kubeconfig's exec credential plugins run in the sidecar, never in the
// sandbox, so none is listed.
var CuratedExecutables = []Executable{
	{Name: "kubectl", Invocation: "kubectl version --client"},
	{Name: "helm", Invocation: "helm version"},
	{Name: "kustomize", Invocation: "kustomize version"},
	{Name: "git", Invocation: "git --version"},
	{Name: "jq", Invocation: "jq --version"},
	{Name: "yq", Invocation: "yq --version"},
}

// FieldExecutables is the registered executables' JSON key.
const FieldExecutables = "executables"

const (
	maxInvocationFields = 8
	maxInvocationField  = 255
)

// executableName is a plain program name: 1 to 64 bytes of [A-Za-z0-9._+-], not
// starting with -.
var executableName = regexp.MustCompile(`^[A-Za-z0-9._+][A-Za-z0-9._+-]{0,63}$`)

// CheckExecutable is why an executable named name, probed by invocation, cannot be
// registered, a ExecutableRefusal, or nil. The invocation is split on whitespace
// and never run through a shell, so its first field must be the name.
func CheckExecutable(name, invocation string) error {
	fields := strings.Fields(invocation)
	switch {
	case !executableName.MatchString(name) || name == "." || name == "..":
		return ExecutableRefusal("An executable's name is 1 to 64 letters, digits, ., _, + or -, and does not start with -.")
	case slices.ContainsFunc(CuratedExecutables, func(curated Executable) bool { return curated.Name == name }):
		return ExecutableRefusal("Kstack probes " + name + " already.")
	case len(fields) == 0 || len(fields) > maxInvocationFields:
		return ExecutableRefusal("An invocation is 1 to 8 words.")
	case fields[0] != name:
		return ExecutableRefusal("An invocation starts with the executable's name.")
	}
	for _, field := range fields {
		if len(field) > maxInvocationField || strings.ContainsFunc(field, unicode.IsControl) {
			return ExecutableRefusal("Each word of an invocation is under 256 bytes, with no control character.")
		}
	}
	return nil
}

// checkExecutables refuses an executable CheckExecutable refuses, or one whose name an earlier
// executable has. An executable only widens what is probed, so a refused one is dropped.
func checkExecutables(v *Settings) []Refusal {
	var refused []Refusal
	var kept []Executable
	for _, executable := range v.Executables {
		err := CheckExecutable(executable.Name, executable.Invocation)
		if err == nil && slices.ContainsFunc(kept, func(k Executable) bool { return k.Name == executable.Name }) {
			err = ExecutableRefusal("It is listed twice.")
		}
		if err != nil {
			refused = append(refused, Refusal{Field: FieldExecutables, Value: executable.Name, Reason: err.Error()})
			continue
		}
		kept = append(kept, executable)
	}
	v.Executables = kept
	return refused
}

// A ExecutableRefusal is a register or a remove the user cannot have, in their words.
type ExecutableRefusal string

func (r ExecutableRefusal) Error() string { return string(r) }

const (
	ErrExecutableListed        ExecutableRefusal = "That executable is already listed."
	ErrExecutableNotRegistered ExecutableRefusal = "That executable is not registered."
)

// RegisterExecutable adds an executable for the probe to check, with invocation, or
// "<name> --version" when it is "".
func (s *Store) RegisterExecutable(name, invocation string) error {
	if invocation == "" {
		invocation = name + " --version"
	}
	if err := CheckExecutable(name, invocation); err != nil {
		return err
	}
	return s.Update(func(v *Settings) error {
		if slices.ContainsFunc(v.Executables, func(executable Executable) bool { return executable.Name == name }) {
			return ErrExecutableListed
		}
		v.Executables = append(v.Executables, Executable{Name: name, Invocation: invocation})
		return nil
	})
}

// RemoveExecutable removes a registered executable by name.
func (s *Store) RemoveExecutable(name string) error {
	return s.Update(func(v *Settings) error {
		i := slices.IndexFunc(v.Executables, func(executable Executable) bool { return executable.Name == name })
		if i < 0 {
			return ErrExecutableNotRegistered
		}
		v.Executables = slices.Delete(v.Executables, i, i+1)
		return nil
	})
}
