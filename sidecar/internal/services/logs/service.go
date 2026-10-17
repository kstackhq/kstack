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

import (
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// Service reads views' lines off the clusters the cluster service connects to, and
// keeps them in each cluster cache's log file.
type Service struct {
	clusters cluster.Service
	files    *logstore.Manager
	// backends are the cluster's backends in the order a gap is offered to them,
	// each built over a request's claim: the kubelet, or a test's fakes. A follow
	// tails through the first.
	backends []namedBackend
	now      func() time.Time
	// handoff is how long a follow holds its first lines, retry how long it waits
	// to resolve its view again after the mirror refused it, to open its watches
	// again or to reopen a dropped stream, retryCap the longest a stream that keeps
	// dropping waits, settle how long it waits after a pod frame, and budget the
	// bytes one page's fills may read from the backends.
	handoff  time.Duration
	retry    time.Duration
	retryCap time.Duration
	settle   time.Duration
	budget   int64
	// walkBudget is the bytes one page's walks of the file may read, apart from what
	// its fills spend, so a fill that spends everything leaves the walks their own.
	walkBudget int64
}

// namedBackend is one of the cluster's backends: its name, which a chunk records as
// its origin, and how to build it over a claim's get.
type namedBackend struct {
	name  string
	build func(get getter) Backend
}

// New is the service over the cluster service and the log files.
func New(clusters cluster.Service, files *logstore.Manager) *Service {
	return &Service{clusters: clusters, files: files, backends: []namedBackend{{"kubelet", newKubelet}}, now: time.Now, handoff: followHandoff, retry: followRetry, retryCap: followRetryCap, settle: followSettle, budget: maxBudget, walkBudget: maxWalkBudget}
}
