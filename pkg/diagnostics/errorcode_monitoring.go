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
	ctx     context.Context
	enabled bool
}

func newErrorCodeMetrics() *errorCodeMetrics {
	return &errorCodeMetrics{ //nolint:exhaustruct
		errorCodeCount: stats.Int64(
			"error_code/count",
			"Number of times an error with a specific errorcode was encountered.",
			stats.UnitDimensionless),

		ctx:     context.Background(),
		enabled: false,
	}
}

// Init registers the errorcode metrics view.
func (m *errorCodeMetrics) Init(id string) error {
	m.enabled = true
	m.appID = id

	return view.Register(
		diagUtils.NewMeasureView(m.errorCodeCount, []tag.Key{appIDKey, errorCodeKey, typeKey}, view.Count()),
	)
}

func (m *errorCodeMetrics) RecordErrorCode(ec errorcodes.ErrorCode) {
	if m.enabled {
		_ = stats.RecordWithTags(
			m.ctx,
			diagUtils.WithTags(m.errorCodeCount.Name(), appIDKey, m.appID, errorCodeKey, ec.Code, typeKey, ec.Category),
			m.errorCodeCount.M(1),
		)
	}
}