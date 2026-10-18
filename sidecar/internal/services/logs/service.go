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
	"sync"
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
	// retry is how long a follow waits to resolve its view again after the mirror
	// refused it, to open its watches again or to tail a dropped stream again,
	// retryCap the longest a stream that keeps dropping waits, settle how long it
	// waits after a pod frame.
	retry    time.Duration
	retryCap time.Duration
	settle   time.Duration
	// caps is the limits a page's and a follow's fills keep to: defaultCaps, or a
	// test's.
	caps caps
	// walkBudget is the bytes one page's walks of the file may read, apart from what
	// its fills downloaded.
	walkBudget int64
	// grace is how long a tailer runs on after its last follow lets go: the files'
	// TailGrace, or a test's.
	grace time.Duration

	// afterFill runs once a page's or a follow's fills have committed and before its
	// walk: a test's, so a sweep can land between them; nil in production.
	afterFill func()

	// tails is each log file's tailers, by cache id.
	tailsMu sync.Mutex
	tails   map[cluster.ClusterCacheID]*registry
}

// namedBackend is one of the cluster's backends: its name, which a chunk records as
// its origin, and how to build it over a claim's get.
type namedBackend struct {
	name  string
	build func(get getter) Backend
}

// New is the service over the cluster service, keeping its log files under dir to
// ret, whose sweeps it tells which pods are gone. It owes a Close.
func New(clusters cluster.Service, dir string, ret logstore.Retention) *Service {
	files := logstore.NewManager(dir, ret)
	s := &Service{
		clusters: clusters, files: files, backends: []namedBackend{{"kubelet", newKubelet}}, now: time.Now,
		retry: followRetry, retryCap: followRetryCap, settle: followSettle,
		caps: defaultCaps, walkBudget: maxWalkBudget, grace: ret.TailGrace,
		tails: map[cluster.ClusterCacheID]*registry{},
	}
	files.SetPodsGone(s.podsGone)
	return s
}

// Close closes the log files. The cluster service's cache pass may still clear or
// remove one after it.
func (s *Service) Close() error { return s.files.Close() }

// Clear empties the cache cacheID's log file, and Remove deletes it for good: the
// cluster service calls them with the cache's clear and removal.
func (s *Service) Clear(cacheID int64) error  { return s.files.Clear(cacheID) }
func (s *Service) Remove(cacheID int64) error { return s.files.Remove(cacheID) }

var _ cluster.CacheFileOwner = (*Service)(nil)
