package monitoring

import (
	"context"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/logger"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	// Metrics definitions
	csrReceivedTotal = stats.Int64(
		"sentry/cert/sign/request_received_total",
		"The number of CSRs received.",
		stats.UnitDimensionless)

	nilKey = []tag.Key{}

	// Metrics Tags
	errorTypeTag tag.Key
)

var log = logger.NewLogger("dapr.runtime.monitoring")

func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// CertSignRequestRecieved counts when CSR received.
func CertSignRequestRecieved() {
	stats.Record(context.Background(), csrReceivedTotal.M(1))
}

// InitMetrics initializes metrics
func InitMetrics() error {
	var err error
	if errorTypeTag, err = tag.NewKey("error_type"); err != nil {
		return fmt.Errorf("failed to create new key: %v", err)
	}

	err = view.Register(
		newView(csrReceivedTotal, nilKey, view.Count()),
	)

	return err
}
