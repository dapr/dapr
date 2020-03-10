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
	appID   = "app_id"
	podName = "pod_name"
)

var (
	successfulAdmissionReviewResponseTotal = stats.Int64(
		"injector/successful_admissionreview_response_total",
		"The total number of successful admission review responses.",
		stats.UnitDimensionless)
	failedAdmissionReviewResponseTotal = stats.Int64(
		"injector/failed_admissionreview_response_total",
		"The total number of failed admission review responses.",
		stats.UnitDimensionless)

	// appIDKey is a tag key for App ID
	appIDKey = tag.MustNewKey(appID)

	// podNameKey is a tag key for Pod name
	podNameKey = tag.MustNewKey(podName)
)

// RecordSuccessfulAdmissionReviewResponseCount records the number of successful admission review responses
func RecordSuccessfulAdmissionReviewResponseCount(appID, podname string) {
	stats.RecordWithTags(context.Background(), withTags(appIDKey, appID, podNameKey, podname), successfulAdmissionReviewResponseTotal.M(1))
}

// RecordFailedAdmissionReviewResponseCount records the number of failed admission review responses
func RecordFailedAdmissionReviewResponseCount(appID, podname string) {
	stats.RecordWithTags(context.Background(), withTags(appIDKey, appID, podNameKey, podname), failedAdmissionReviewResponseTotal.M(1))
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

// InitMetrics initialize the injector service metrics
func InitMetrics() error {
	err := view.Register(
		newView(successfulAdmissionReviewResponseTotal, []tag.Key{appIDKey, podNameKey}, view.Count()),
		newView(failedAdmissionReviewResponseTotal, []tag.Key{appIDKey, podNameKey}, view.Count()),
	)

	return err
}
