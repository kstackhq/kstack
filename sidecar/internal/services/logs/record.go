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
// stamped it, the message cleaned by stripControl, where it came from, and its
// Ordinal, its index among its container's lines at that moment, which a cursor
// carries and the wire does not. Every string came from the cluster.
type Record struct {
	Timestamp time.Time
	Message   string
	Source    RecordSource
	Ordinal   int
}

// RecordSource is where a line came from: the container of a pod, whether it is
// the instance before the last restart, and the node the pod ran on with the
// node's region, zone, OS and architecture, empty where the mirror does not know.
type RecordSource struct {
	Namespace string
	Pod       string
	Container string
	Previous  bool
	Node      string
	Region    string
	Zone      string
	OS        string
	Arch      string
}

// Page is a page of a view's lines, oldest first. NextCursor is set while there are
// more in the direction asked, and Skipped names each stream the page could not
// read, one line each.
type Page struct {
	Records    []Record
	NextCursor *string
	Skipped    []string
}

// FetchMode is which end of the range a page is cut from when it holds more than
// its limit. The GraphQL LogsFetchMode enum binds member by member onto it.
type FetchMode string

const (
	FetchHead FetchMode = "head"
	FetchTail FetchMode = "tail"
)

// Cursor is the cursor naming rec, which a fetch or a follow resumes past.
func (rec Record) Cursor() string { return *cursorOf(positionOf(rec)) }

// FollowFrame is one frame of a follow: a line, or the follow's end, with the
// reason in the user's words. The end is the last frame.
type FollowFrame struct {
	Type   FollowFrameType
	Record *Record
	Reason string
}

// FollowFrameType is what a follow frame carries. The GraphQL LogsFollowFrameType
// enum binds member by member onto it.
type FollowFrameType string

const (
	FollowLine  FollowFrameType = "line"
	FollowEnded FollowFrameType = "ended"
)
