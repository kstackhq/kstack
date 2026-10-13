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

package logs

import "time"

// Record is one log line as the sidecar read it: when the container runtime
// stamped it, the message cleaned by stripControl, the log it came from and where
// that log's pod runs, and its Ordinal, its index among its stream's lines at that
// moment, which a cursor carries and the wire does not. Every string came from the
// cluster.
type Record struct {
	Timestamp time.Time
	Message   string
	Stream    StreamKey
	Place     Place
	Ordinal   int
}

// StreamKey names one log a line came from: a pod's container, and whether it is
// the instance before the last restart.
type StreamKey struct {
	Namespace string
	Pod       string
	Container string
	Previous  bool
}

// Place is where a pod runs: its node and the node's region, zone, OS and
// architecture, empty where the mirror does not know. It describes a stream and
// takes no part in naming it.
type Place struct {
	Node   string
	Region string
	Zone   string
	OS     string
	Arch   string
}

// Cursor is the cursor naming rec, which a fetch or a follow resumes past.
func (rec Record) Cursor() string { return *cursorOf(positionOf(rec)) }
