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

package kubequery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kstackhq/kstack/sidecar/internal/clustercard"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// answer is one query's answer: the name the cluster goes by, the card's freshness
// section, and the rows unless the verdict withholds them.
type answer struct {
	// cluster is the display name the user set, else the kube-context.
	cluster   string
	freshness json.RawMessage
	withheld  bool
	rows      cluster.ClusterCachedDataQueryResult
}

// errNoCache is a cluster with no cache to read: its record gone, its identity
// moving under both readings, or its cache gone under the reads.
var errNoCache = errors.New("kubequery: the cluster has no cache")

// query runs sql over clusterID's active cache inside one reading of the cluster, so
// the verdict and the rows describe one cache.
func (t *Tool) query(ctx context.Context, clusterID cluster.ClusterID, sql string, maxRows int) (answer, error) {
	var a answer
	err := t.svc.Clusters().ReadActive(ctx, clusterID, func(ctx context.Context, active cluster.ActiveCluster) error {
		var err error
		a, err = t.read(ctx, clusterID, active, sql, maxRows)
		return err
	})
	if errors.Is(err, cluster.ErrNotFound) || errors.Is(err, cluster.ErrIdentityMoved) {
		return answer{}, errNoCache
	}
	return a, err
}

// read is one reading's answer: the cluster's name, the verdict off its active
// cache's health, and the rows unless the verdict withholds them.
func (t *Tool) read(ctx context.Context, clusterID cluster.ClusterID, active cluster.ActiveCluster, sql string, maxRows int) (answer, error) {
	a := answer{cluster: active.Cluster.KubeContext()}
	if name := active.Cluster.Spec.Name; name != nil && *name != "" {
		a.cluster = *name
	}
	// No cache is syncing, which withholds, so the query below always has a cache.
	fresh := clustercard.Freshness(nil)
	if active.Cache != nil {
		health, ok, err := t.svc.Caches().Health(ctx, clusterID, active.Cache.ID)
		if err != nil {
			return answer{}, err
		}
		if !ok {
			return answer{}, errNoCache
		}
		fresh = clustercard.Freshness(&health)
	}
	a.freshness = clustercard.MarshalFreshness(fresh)
	if fresh.Withholds() {
		a.withheld = true
		return a, nil
	}
	rows, ok, err := t.svc.CachedData().Query(ctx, clusterID, active.Cache.ID, sql, maxRows, tools.FileLimit)
	if err != nil {
		return answer{}, err
	}
	if !ok {
		return answer{}, errNoCache
	}
	a.rows = rows
	return a, nil
}
