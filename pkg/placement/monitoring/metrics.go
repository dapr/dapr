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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	runtimesTotal           metric.Int64Counter
	actorRuntimesTotal      metric.Int64Counter
	actorHeartbeatTimestamp metric.Int64Counter

	// Metrics tags
	appIDKey     = "app_id"
	actorTypeKey = "actor_type"
	hostNameKey  = "host_name"
	podNameKey   = "pod_name"
)

// RecordRuntimesCount records the number of connected runtimes.
func RecordRuntimesCount(count int) {
	runtimesTotal.Add(context.Background(), int64(count))
}

// RecordActorRuntimesCount records the number of valid actor runtimes.
func RecordActorRuntimesCount(count int) {
	actorRuntimesTotal.Add(context.Background(), int64(count))
}

// RecordActorHeartbeat records the actor heartbeat, in seconds since epoch, with actor type, host and pod name.
func RecordActorHeartbeat(appID, actorType, host, pod string, heartbeatTime time.Time) {
	actorHeartbeatTimestamp.Add(context.Background(), heartbeatTime.Unix(), metric.WithAttributes(attribute.String(appIDKey, appID), attribute.String(actorTypeKey, actorType), attribute.String(hostNameKey, host), attribute.String(podNameKey, pod)))
}

// InitMetrics initialize the placement service metrics.
func InitMetrics() error {
	m := otel.Meter("placement")

	var err error
	runtimesTotal, err = m.Int64Counter(
		"placement.runtimes_total",
		metric.WithDescription("The total number of runtimes reported to placement service."),
	)
	if err != nil {
		return err
	}

	actorRuntimesTotal, err = m.Int64Counter(
		"placement.actor_runtimes_total",
		metric.WithDescription("The total number of actor runtimes reported to placement service."),
	)
	if err != nil {
		return err
	}

	actorHeartbeatTimestamp, err = m.Int64Counter(
		"placement.actor_heartbeat_timestamp",
		metric.WithDescription("The actor's heartbeat timestamp (in seconds) was last reported to the placement service."),
	)
	if err != nil {
		return err
	}

	return nil
}
