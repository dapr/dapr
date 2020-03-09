// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

const (
	appID = "app_id"
)

var (
	serviceCreatedTotal = stats.Int64(
		"operator/service_created_total",
		"The total number of dapr service created.",
		stats.UnitDimensionless)
	serviceDeletedTotal = stats.Int64(
		"operator/service_deleted_total",
		"The total number of dapr service deleted.",
		stats.UnitDimensionless)
	serviceUpdatedTotal = stats.Int64(
		"operator/service_updated_total",
		"The total number of dapr service updated.",
		stats.UnitDimensionless)

	// appIDKey is a tag key for App ID
	appIDKey = tag.MustNewKey(appID)
)

// RecordServiceCreatedCount records the number of dapr service created
func RecordServiceCreatedCount(appID string) {
	stats.RecordWithTags(context.Background(), withTags(appIDKey, appID), serviceCreatedTotal.M(1))
}

// RecordServiceDeletedCount records the number of dapr service deleted
func RecordServiceDeletedCount(appID string) {
	stats.RecordWithTags(context.Background(), withTags(appIDKey, appID), serviceDeletedTotal.M(1))
}

// RecordServiceUpdatedCount records the number of dapr service updated
func RecordServiceUpdatedCount(appID string) {
	stats.RecordWithTags(context.Background(), withTags(appIDKey, appID), serviceUpdatedTotal.M(1))
}

// withTags converts tag key and value pairs to tag.Mutator array.
// withTags(key1, value1, key2, value2) returns
// []tag.Mutator{tag.Upsert(key1, value1), tag.Upsert(key2, value2)}
func withTags(opts ...interface{}) []tag.Mutator {
	tagMutators := []tag.Mutator{}
	for i := 0; i < len(opts)-1; i += 2 {
		key, ok := opts[i].(tag.Key)
		if !ok {
			break
		}
		value, ok := opts[i+1].(string)
		if !ok {
			break
		}
		tagMutators = append(tagMutators, tag.Upsert(key, value))
	}
	return tagMutators
}

func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// InitMetrics initialize the placement service metrics
func InitMetrics() error {
	err := view.Register(
		newView(serviceCreatedTotal, []tag.Key{appIDKey}, view.Count()),
		newView(serviceDeletedTotal, []tag.Key{appIDKey}, view.Count()),
		newView(serviceUpdatedTotal, []tag.Key{appIDKey}, view.Count()),
	)

	return err
}
