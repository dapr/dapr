package monitoring

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	// Metrics definitions.
	csrReceivedTotal = stats.Int64(
		"sentry/cert/sign/request_received_total",
		"The number of CSRs received.",
		stats.UnitDimensionless)
	certSignSuccessTotal = stats.Int64(
		"sentry/cert/sign/success_total",
		"The number of certificates issuances that have succeeded.",
		stats.UnitDimensionless)
	certSignFailedTotal = stats.Int64(
		"sentry/cert/sign/failure_total",
		"The number of errors occurred when signing the CSR.",
		stats.UnitDimensionless)
	serverTLSCertIssueFailedTotal = stats.Int64(
		"sentry/servercert/issue_failed_total",
		"The number of server TLS certificate issuance failures.",
		stats.UnitDimensionless)
	issuerCertChangedTotal = stats.Int64(
		"sentry/issuercert/changed_total",
		"The number of issuer cert updates, when issuer cert or key is changed",
		stats.UnitDimensionless)
	issuerCertExpiryTimestamp = stats.Int64(
		"sentry/issuercert/expiry_timestamp",
		"The unix timestamp, in seconds, when issuer/root cert will expire.",
		stats.UnitDimensionless)

	// Metrics Tags.
	failedReasonKey = tag.MustNewKey("reason")
	noKeys          = []tag.Key{}
)

// CertSignRequestReceived counts when CSR received.
func CertSignRequestReceived() {
	stats.Record(context.Background(), csrReceivedTotal.M(1))
}

// CertSignSucceed counts succeeded cert issuance.
func CertSignSucceed() {
	stats.Record(context.Background(), certSignSuccessTotal.M(1))
}

// CertSignFailed counts succeeded cert issuance.
func CertSignFailed(reason string) {
	stats.RecordWithTags(
		context.Background(),
		diag_utils.WithTags(failedReasonKey, reason),
		certSignFailedTotal.M(1))
}

// IssuerCertExpiry records root cert expiry.
func IssuerCertExpiry(expiry time.Time) {
	stats.Record(context.Background(), issuerCertExpiryTimestamp.M(expiry.Unix()))
}

// ServerCertIssueFailed records server cert issue failure.
func ServerCertIssueFailed(reason string) {
	stats.Record(context.Background(), serverTLSCertIssueFailedTotal.M(1))
}

// IssuerCertChanged records issuer credential change.
func IssuerCertChanged() {
	stats.Record(context.Background(), issuerCertChangedTotal.M(1))
}

// InitMetrics initializes metrics.
func InitMetrics() error {
	return view.Register(
		diag_utils.NewMeasureView(csrReceivedTotal, noKeys, view.Count()),
		diag_utils.NewMeasureView(certSignSuccessTotal, noKeys, view.Count()),
		diag_utils.NewMeasureView(certSignFailedTotal, []tag.Key{failedReasonKey}, view.Count()),
		diag_utils.NewMeasureView(serverTLSCertIssueFailedTotal, []tag.Key{failedReasonKey}, view.Count()),
		diag_utils.NewMeasureView(issuerCertChangedTotal, noKeys, view.Count()),
		diag_utils.NewMeasureView(issuerCertExpiryTimestamp, noKeys, view.LastValue()),
	)
}
