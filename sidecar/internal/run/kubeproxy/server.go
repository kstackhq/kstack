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
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 64 << 10
)

// NewServer is the server a run serves its grant on. Any local process can
// reach a forwarder's port, so a connection that never sends its headers, or
// sits idle between requests, is closed rather than held for the run's life.
// It sets no read or write timeout, which would cut a watch; a watch is not
// idle while it streams.
func NewServer(g *Grant) *http.Server {
	return newServer(g, readHeaderTimeout, idleTimeout)
}

func newServer(g *Grant, readHeader, idle time.Duration) *http.Server {
	return &http.Server{
		Handler:           g,
		ReadHeaderTimeout: readHeader,
		IdleTimeout:       idle,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}
