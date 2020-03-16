// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	hostsTotal = stats.Int64(
		"placement/hosts_total",
		"The total number of hosts reported to placement service.",
		stats.UnitDimensionless)
	actorTypesTotal = stats.Int64(
		"placement/actortypes_total",
		"The total number of actor types reported to placement service.",
		stats.UnitDimensionless)
	nonActorHostsTotal = stats.Int64(
		"placement/nonactorhosts_total",
		"The total number of non actor hosts reported to placement service.",
		stats.UnitDimensionless)
	replicasPerActorTypeTotal = stats.Int64(
		"placement/replicas_peractortype_total",
		"The total number of replicas per actor type reported to placement service.",
		stats.UnitDimensionless)

	noKeys       = []tag.Key{}
	actorTypeKey = tag.MustNewKey("actor_type")
	hostNameKey  = tag.MustNewKey("host_name")
)

// RecordHostsCount records the number of hosts
func RecordHostsCount(count int) {
	stats.Record(context.Background(), hostsTotal.M(int64(count)))
}

// RecordActorTypesCount records the number of actor types
func RecordActorTypesCount(count int) {
	stats.Record(context.Background(), actorTypesTotal.M(int64(count)))
}

// RecordNonActorHostsCount records the number of non actor hosts
func RecordNonActorHostsCount(count int) {
	stats.Record(context.Background(), nonActorHostsTotal.M(int64(count)))
}

// RecordPerActorTypeReplicasCount records the number of replicas per actor type
func RecordPerActorTypeReplicasCount(actorType, hostName string) {
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(actorTypeKey, actorType, hostNameKey, hostName), replicasPerActorTypeTotal.M(1))
}

// InitMetrics initialize the placement service metrics
func InitMetrics() error {
	err := view.Register(
		diag_utils.NewMeasureView(hostsTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(actorTypesTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(nonActorHostsTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(replicasPerActorTypeTotal, []tag.Key{actorTypeKey, hostNameKey}, view.Count()),
	)

	return err
}
