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
)

// Service reads views' lines off the clusters the cluster service connects to.
type Service struct {
	clusters cluster.Service
	now      func() time.Time
	// handoff is how long a follow holds its first lines, retry how long it waits
	// to resolve its view again after the mirror refused it, to open its watches
	// again or to reopen a dropped stream, retryCap the longest a stream that keeps
	// dropping waits, settle how long it waits after a pod frame, and budget the
	// bytes one fetch may read.
	handoff  time.Duration
	retry    time.Duration
	retryCap time.Duration
	settle   time.Duration
	budget   int64
}

// New is the service over the cluster service.
func New(clusters cluster.Service) *Service {
	return &Service{clusters: clusters, now: time.Now, handoff: followHandoff, retry: followRetry, retryCap: followRetryCap, settle: followSettle, budget: maxBudget}
}
