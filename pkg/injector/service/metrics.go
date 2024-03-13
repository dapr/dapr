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

package service

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	appID        = "app_id"
	failedReason = "reason"
)

var (
	sidecarInjectionRequestsTotal metric.Int64Counter
	succeededSidecarInjectedTotal metric.Int64Counter
	failedSidecarInjectedTotal    metric.Int64Counter

	// appIDKey is a tag key for App ID.
	appIDKey = appID

	// failedReasonKey is a tag key for failed reason.
	failedReasonKey = failedReason
)

// RecordSidecarInjectionRequestsCount records the total number of sidecar injection requests.
func RecordSidecarInjectionRequestsCount() {
	sidecarInjectionRequestsTotal.Add(context.Background(), 1)
}

// RecordSuccessfulSidecarInjectionCount records the number of successful sidecar injections.
func RecordSuccessfulSidecarInjectionCount(appID string) {
	succeededSidecarInjectedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(appIDKey, appID)))
}

// RecordFailedSidecarInjectionCount records the number of failed sidecar injections.
func RecordFailedSidecarInjectionCount(appID, reason string) {
	failedSidecarInjectedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(appIDKey, appID), attribute.String(failedReasonKey, reason)))
}

// InitMetrics initialize the injector service metrics.
func InitMetrics() error {
	m := otel.Meter("injector")

	var err error
	sidecarInjectionRequestsTotal, err = m.Int64Counter(
		"injector.sidecar_injection.requests_total",
		metric.WithDescription("The total number of sidecar injection requests."),
	)
	if err != nil {
		return err
	}

	succeededSidecarInjectedTotal, err = m.Int64Counter(
		"injector.sidecar_injection.succeeded_total",
		metric.WithDescription("The total number of successful sidecar injections."),
	)
	if err != nil {
		return err
	}

	failedSidecarInjectedTotal, err = m.Int64Counter(
		"injector.sidecar_injection.failed_total",
		metric.WithDescription("The total number of failed sidecar injections."),
	)
	if err != nil {
		return err
	}

	return nil
}
