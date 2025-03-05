/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package monitoring

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	noKeys = []tag.Key{}

	runtimesTotal = stats.Int64(
		"placement/runtimes_total",
		"The total number of runtimes reported to placement service.",
		stats.UnitDimensionless)
	actorRuntimesTotal = stats.Int64(
		"placement/actor_runtimes_total",
		"The total number of actor runtimes reported to placement service.",
		stats.UnitDimensionless)

	actorHeartbeatTimestamp = stats.Int64(
		"placement/actor_heartbeat_timestamp",
		"The actor's heartbeat timestamp (in seconds) was last reported to the placement service.",
		stats.UnitDimensionless)

	leaderStatus = stats.Int64(
		"placement/leader_status",
		"Placement server leadership status (1 for leader, 0 for not leader).",
		stats.UnitDimensionless)

	raftLeaderStatus = stats.Int64(
		"placement/raft_leader_status",
		"Raft server leadership status (1 for leader, 0 for not leader).",
		stats.UnitDimensionless)

	// Metrics tags
	appIDKey     = tag.MustNewKey("app_id")
	actorTypeKey = tag.MustNewKey("actor_type")
	hostNameKey  = tag.MustNewKey("host_name")
	namespaceKey = tag.MustNewKey("host_namespace")
	podNameKey   = tag.MustNewKey("pod_name")
)

// RecordRuntimesCount records the number of connected runtimes.
func RecordRuntimesCount(count int, ns string) {
	stats.RecordWithTags(
		context.Background(),
		diagUtils.WithTags(actorHeartbeatTimestamp.Name(), namespaceKey, ns),
		runtimesTotal.M(int64(count)),
	)
}

// RecordActorRuntimesCount records the number of actor-hosting runtimes.
func RecordActorRuntimesCount(count int, ns string) {
	stats.RecordWithTags(
		context.Background(),
		diagUtils.WithTags(actorHeartbeatTimestamp.Name(), namespaceKey, ns),
		actorRuntimesTotal.M(int64(count)),
	)
}

// RecordActorHeartbeat records the actor heartbeat, in seconds since epoch, with actor type, host and pod name.
func RecordActorHeartbeat(appID, actorType, host, namespace, pod string, heartbeatTime time.Time) {
	stats.RecordWithTags(
		context.Background(),
		diagUtils.WithTags(actorHeartbeatTimestamp.Name(), appIDKey, appID, actorTypeKey, actorType, hostNameKey, host, namespaceKey, namespace, podNameKey, pod),
		actorHeartbeatTimestamp.M(heartbeatTime.Unix()))
}

// RecordPlacementLeaderStatus records the leader status of the placement server.
func RecordPlacementLeaderStatus(isLeader bool) {
	status := int64(0)
	if isLeader {
		status = 1
	}
	stats.Record(
		context.Background(),
		leaderStatus.M(status),
	)
}

// RecordRaftPlacementLeaderStatus records the leader status of the raft server.
func RecordRaftPlacementLeaderStatus(isLeader bool) {
	status := int64(0)
	if isLeader {
		status = 1
	}
	stats.Record(
		context.Background(),
		raftLeaderStatus.M(status),
	)
}

// InitMetrics initialize the placement service metrics.
func InitMetrics() error {
	err := view.Register(
		diagUtils.NewMeasureView(runtimesTotal, []tag.Key{namespaceKey}, view.LastValue()),
		diagUtils.NewMeasureView(actorRuntimesTotal, []tag.Key{namespaceKey}, view.LastValue()),
		diagUtils.NewMeasureView(actorHeartbeatTimestamp, []tag.Key{appIDKey, actorTypeKey, hostNameKey, namespaceKey, podNameKey}, view.LastValue()),
		diagUtils.NewMeasureView(leaderStatus, noKeys, view.LastValue()),
		diagUtils.NewMeasureView(raftLeaderStatus, noKeys, view.LastValue()),
	)

	RecordPlacementLeaderStatus(false)
	RecordRaftPlacementLeaderStatus(false)

	return err
}
