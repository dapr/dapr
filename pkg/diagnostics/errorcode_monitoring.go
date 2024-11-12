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
	"github.com/dapr/dapr/pkg/messages/errorcodes"
)

type errorCodeMetrics struct {
	errorCodeCount *stats.Int64Measure

	appID   string
	enabled bool
}

func newErrorCodeMetrics() *errorCodeMetrics {
	return &errorCodeMetrics{ //nolint:exhaustruct
		errorCodeCount: stats.Int64(
			"error_code/count",
			"Number of times an error with a specific errorcode was encountered.",
			stats.UnitDimensionless),

		enabled: false,
	}
}

// Init registers the errorcode metrics view.
func (m *errorCodeMetrics) Init(id string) error {
	m.enabled = true
	m.appID = id

	return view.Register(
		diagUtils.NewMeasureView(m.errorCodeCount, []tag.Key{appIDKey, errorCodeKey, categoryKey}, view.Count()),
	)
}

func (m *errorCodeMetrics) RecordErrorCode(ec errorcodes.ErrorCode) {
	if m.enabled {
		_ = stats.RecordWithTags(
			context.TODO(),
			diagUtils.WithTags(m.errorCodeCount.Name(), appIDKey, m.appID, errorCodeKey, ec.Code, categoryKey, ec.Category),
			m.errorCodeCount.M(1),
		)
	}
}

// RecordJobErrorCode() is used specifically for compsite Jobs API errors which do not follow the older error tag format from the package `errorcodes`
func (m *errorCodeMetrics) RecordJobErrorCode(compositeJobErrorCode string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			context.TODO(),
			diagUtils.WithTags(m.errorCodeCount.Name(), appIDKey, m.appID, errorCodeKey, compositeJobErrorCode, categoryKey, errorcodes.CategoryJob),
			m.errorCodeCount.M(1),
		)
	}
}
