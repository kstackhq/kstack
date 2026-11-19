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

// Package signer holds what every connector's signer shares.
package signer

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
)

// Covers reports whether u is base or under it: the same scheme, the host
// without case, the same effective port, and a path that is base's or
// continues it after a slash.
func Covers(base, u *url.URL) bool {
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Hostname(), base.Hostname()) || effectivePort(u) != effectivePort(base) {
		return false
	}
	path := strings.TrimSuffix(base.Path, "/")
	return u.Path == path || strings.HasPrefix(u.Path, path+"/")
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// Dead is a binding made with no session: it never signs.
type Dead struct{}

func (Dead) Sign(*http.Request) error { return llm.ErrSignedOut }
func (Dead) Refused(*http.Request)    {}
