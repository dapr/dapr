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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	appID = "app_id"
)

var (
	serviceCreatedTotal metric.Int64Counter
	serviceDeletedTotal metric.Int64Counter
	serviceUpdatedTotal metric.Int64Counter

	// appIDKey is a tag key for App ID.
	appIDKey = appID
)

// RecordServiceCreatedCount records the number of dapr service created.
func RecordServiceCreatedCount(appID string) {
	serviceCreatedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(appIDKey, appID)))
}

// RecordServiceDeletedCount records the number of dapr service deleted.
func RecordServiceDeletedCount(appID string) {
	serviceDeletedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(appIDKey, appID)))
}

// RecordServiceUpdatedCount records the number of dapr service updated.
func RecordServiceUpdatedCount(appID string) {
	serviceUpdatedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(appIDKey, appID)))
}

// InitMetrics initialize the operator service metrics.
func InitMetrics() error {
	m := otel.Meter("operator")

	var err error
	serviceCreatedTotal, err = m.Int64Counter(
		"operator.service_created_total",
		metric.WithDescription("The total number of dapr services created."),
	)
	if err != nil {
		return err
	}

	serviceDeletedTotal, err = m.Int64Counter(
		"operator.service_deleted_total",
		metric.WithDescription("The total number of dapr services deleted."),
	)
	if err != nil {
		return err
	}

	serviceUpdatedTotal, err = m.Int64Counter(
		"operator.service_updated_total",
		metric.WithDescription("The total number of dapr services updated."),
	)
	if err != nil {
		return err
	}

	return nil
}
