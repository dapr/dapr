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
	"errors"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	kitErrors "github.com/dapr/kit/errors"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
		if ec.Code == "" || ec.Category == "" {
			log.Warnf("ErrorCode is malformed: Code = %s, Category = %s", ec.Code, ec.Category)
			return
		}
		_ = stats.RecordWithTags(
			context.TODO(),
			diagUtils.WithTags(m.errorCodeTotal.Name(), appIDKey, m.appID, errorCodeKey, ec.Code, categoryKey, string(ec.Category)),
			m.errorCodeTotal.M(1),
		)
	}
}

// RecordErrorCode is called at the end/middleware of HTTP/gRPC calls and will attempt to find the ErrorCode in an error and record it
func RecordErrorCode(err error) bool {
	var errorCode *errorcodes.ErrorCode
	if ok := errors.As(err, &errorCode); ok {
		DefaultErrorCodeMonitoring.RecordErrorCode(*errorCode)
		return true
	}

	// If not containing ErrorCode failed, its probably a gRPC related kit error with code and category within
	if kitErr, ok := kitErrors.FromError(err); ok {
		DefaultErrorCodeMonitoring.RecordErrorCode(
			errorcodes.ErrorCode{
				Code:     kitErr.ErrorCode(),
				Category: errorcodes.Category(kitErr.Category()),
			})
		return true
	}

	return false
}
