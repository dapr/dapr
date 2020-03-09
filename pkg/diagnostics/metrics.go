// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	// appIDKey is a tag key for App ID
	appIDKey = tag.MustNewKey("app_id")
)

var (
	// DefaultReportingPeriod is the default view reporting period
	DefaultReportingPeriod = 1 * time.Minute

	// DefaultMonitoring holds service monitoring metrics definitions
	DefaultMonitoring = newServiceMetrics()
	// DefaultGRPCMonitoring holds default gRPC monitoring handlers and middleswares
	DefaultGRPCMonitoring = newGRPCMetrics()
	// DefaultHTTPMonitoring holds default HTTP monitoring handlers and middleswares
	DefaultHTTPMonitoring = newHTTPMetrics()
)

// newView creates opencensus View instance using stats.Measure
func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
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

// InitMetrics initializes metrics
func InitMetrics(appID string) error {
	if err := DefaultMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultGRPCMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultHTTPMonitoring.Init(appID); err != nil {
		return err
	}

	// Set reporting period of views
	view.SetReportingPeriod(DefaultReportingPeriod)

	return nil
}
