package monitoring

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	csrReceivedTotal = stats.Int64(
		"sentry/csr/received_total",
		"The number of CSRs received by Dapr Sentry server.",
		stats.UnitDimensionless)
	certIssueSuccessTotal = stats.Int64(
		"sentry/cert/issue/success_total",
		"The number of certificates issuances that have succeeded.",
		stats.UnitDimensionless)
	rootCertExpiryTimestamp = stats.Int64(
		"sentry/rootcert/expiry_timestamp",
		"The unix timestamp, in seconds, when root cert will expire. ",
		stats.UnitDimensionless)
)

func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// CSRRecieved counts when CSR received.
func CSRRecieved() {
	stats.Record(context.Background(), csrReceivedTotal.M(1))
}

// CertIssueSuceeed counts suceeded cert issuance
func CertIssueSuceeed() {
	stats.Record(context.Background(), certIssueSuccessTotal.M(1))
}

// RootCertExpiry records root cert expiry
func RootCertExpiry(expiry time.Time) {
	stats.Record(context.Background(), certIssueSuccessTotal.M(expiry.Unix()))
}

// InitMetrics initializes metrics
func InitMetrics() error {
	err := view.Register(
		newView(csrReceivedTotal, []tag.Key{}, view.Count()),
		newView(certIssueSuccessTotal, []tag.Key{}, view.Count()),
		newView(rootCertExpiryTimestamp, []tag.Key{}, view.LastValue()),
	)

	return err
}
