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
	"go.opencensus.io/tag"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
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
func RecordServiceCreatedCount(metrics *diag.Metrics, appID string) {
	stats.RecordWithOptions(context.Background(),
		metrics.Rules.WithTags(serviceCreatedTotal.Name(), appIDKey, appID),
		stats.WithMeasurements(serviceCreatedTotal.M(1)),
	)
}

// RecordServiceDeletedCount records the number of dapr service deleted.
func RecordServiceDeletedCount(metrics *diag.Metrics, appID string) {
	stats.RecordWithOptions(context.Background(),
		metrics.Rules.WithTags(serviceDeletedTotal.Name(), appIDKey, appID),
		stats.WithMeasurements(serviceDeletedTotal.M(1)),
	)
}

// RecordServiceUpdatedCount records the number of dapr service updated.
func RecordServiceUpdatedCount(metrics *diag.Metrics, appID string) {
	stats.RecordWithOptions(context.Background(),
		metrics.Rules.WithTags(serviceUpdatedTotal.Name(), appIDKey, appID),
		stats.WithMeasurements(serviceUpdatedTotal.M(1)),
	)
}

// InitMetrics initialize the operator service metrics.
func InitMetrics(metrics *diag.Metrics) error {
	return metrics.Meter.Register(
		utils.NewMeasureView(serviceCreatedTotal, []tag.Key{appIDKey}, utils.Count()),
		utils.NewMeasureView(serviceDeletedTotal, []tag.Key{appIDKey}, utils.Count()),
		utils.NewMeasureView(serviceUpdatedTotal, []tag.Key{appIDKey}, utils.Count()),
	)
}
