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

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

const (
	appID = "app_id"
)

var (
	serviceCreatedTotal = stats.Int64(
		"operator/service_created_total",
		"The total number of dapr services created.",
		stats.UnitDimensionless)
	serviceDeletedTotal = stats.Int64(
		"operator/service_deleted_total",
		"The total number of dapr services deleted.",
		stats.UnitDimensionless)
	serviceUpdatedTotal = stats.Int64(
		"operator/service_updated_total",
		"The total number of dapr services updated.",
		stats.UnitDimensionless)

	// appIDKey is a tag key for App ID.
	appIDKey = tag.MustNewKey(appID)
)

// RecordServiceCreatedCount records the number of dapr service created.
func RecordServiceCreatedCount(appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(serviceCreatedTotal.Name(), appIDKey, appID), serviceCreatedTotal.M(1))
}

// RecordServiceDeletedCount records the number of dapr service deleted.
func RecordServiceDeletedCount(appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(serviceDeletedTotal.Name(), appIDKey, appID), serviceDeletedTotal.M(1))
}

// RecordServiceUpdatedCount records the number of dapr service updated.
func RecordServiceUpdatedCount(appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(serviceUpdatedTotal.Name(), appIDKey, appID), serviceUpdatedTotal.M(1))
}

// InitMetrics initialize the operator service metrics.
func InitMetrics() error {
	err := view.Register(
		diagUtils.NewMeasureView(serviceCreatedTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(serviceDeletedTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(serviceUpdatedTotal, []tag.Key{appIDKey}, view.Count()),
	)

	return err
}
