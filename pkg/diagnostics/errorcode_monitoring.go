/*
Copyright 2024 The Dapr Authors
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

package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
)

type errorCodeMetrics struct {
	errorCodeTotal *stats.Int64Measure

	appID   string
	enabled bool
}

func newErrorCodeMetrics() *errorCodeMetrics {
	return &errorCodeMetrics{ //nolint:exhaustruct
		errorCodeTotal: stats.Int64(
			"error_code/total",
			"Total number of times an error with a specific error code was encountered.",
			stats.UnitDimensionless),

		enabled: false,
	}
}

// Init registers the errorcode metrics view.
func (m *errorCodeMetrics) Init(id string) error {
	m.enabled = true
	m.appID = id

	return view.Register(
		diagUtils.NewMeasureView(m.errorCodeTotal, []tag.Key{appIDKey, errorCodeKey, categoryKey}, view.Count()),
	)
}

func (m *errorCodeMetrics) RecordErrorCode(ec errorcodes.ErrorCode) {
	if m.enabled {
		_ = stats.RecordWithTags(
			context.TODO(),
			diagUtils.WithTags(m.errorCodeTotal.Name(), appIDKey, m.appID, errorCodeKey, ec.Code, categoryKey, string(ec.Category)),
			m.errorCodeTotal.M(1),
		)
	}
}

// RecordCompErrorCode() is used specifically for composite errors which do not follow the older error tag format from the package `errorcodes`
func (m *errorCodeMetrics) RecordCompErrorCode(compositeJobErrorCode string, cat errorcodes.Category) {
	if m.enabled {
		_ = stats.RecordWithTags(
			context.TODO(),
			diagUtils.WithTags(m.errorCodeTotal.Name(), appIDKey, m.appID, errorCodeKey, compositeJobErrorCode, categoryKey, string(cat)),
			m.errorCodeTotal.M(1),
		)
	}
}

// RecordAPIErrorCode will record the APIError as a metric
func RecordAPIErrorCode(e messages.APIError) {
	DefaultErrorCodeMonitoring.RecordErrorCode(e.Tag())
}

// TryRecordAPIErrorCode will attempt to record the APIError error as a metric
func TryRecordAPIErrorCode(err error) {
	if err == nil {
		return
	}

	// Check if it's an APIError object
	apiErr, ok := err.(messages.APIError)
	if ok {
		RecordAPIErrorCode(apiErr)
		return
	}
}

// RecordErrorCodeAndGet will record the error as a metric and return the ErrorCode object's code string
func RecordErrorCodeAndGet(errorCode errorcodes.ErrorCode) string {
	DefaultErrorCodeMonitoring.RecordErrorCode(errorCode)
	return errorCode.Code
}

// RecordCompAndGet will record the error as a metric and return the composite code string, not yet compatible with the ErrorCode structure
func RecordCompAndGet(compositeJobErrorCode string, cat errorcodes.Category) string {
	DefaultErrorCodeMonitoring.RecordCompErrorCode(compositeJobErrorCode, cat)
	return compositeJobErrorCode
}
