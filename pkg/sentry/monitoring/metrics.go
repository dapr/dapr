package monitoring

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
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
func CertSignRequestReceived(metrics *diag.Metrics) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(csrReceivedTotal.M(1)),
	)
}

// CertSignSucceed counts succeeded cert issuance.
func CertSignSucceed(metrics *diag.Metrics) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(certSignSuccessTotal.M(1)),
	)
}

// CertSignFailed counts succeeded cert issuance.
func CertSignFailed(metrics *diag.Metrics, reason string) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		metrics.Rules.WithTags(certSignFailedTotal.Name(), failedReasonKey, reason),
		stats.WithMeasurements(certSignFailedTotal.M(1)),
	)
}

// IssuerCertExpiry records root cert expiry.
func IssuerCertExpiry(metrics *diag.Metrics, expiry *time.Time) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(issuerCertExpiryTimestamp.M(expiry.Unix())),
	)
}

// ServerCertIssueFailed records server cert issue failure.
func ServerCertIssueFailed(metrics *diag.Metrics, reason string) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(serverTLSCertIssueFailedTotal.M(1)),
	)
}

// IssuerCertChanged records issuer credential change.
func IssuerCertChanged(metrics *diag.Metrics) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(issuerCertChangedTotal.M(1)),
	)
}

// InitMetrics initializes metrics.
func InitMetrics(metrics *diag.Metrics) error {
	return metrics.Meter.Register(
		utils.NewMeasureView(csrReceivedTotal, noKeys, utils.Count()),
		utils.NewMeasureView(certSignSuccessTotal, noKeys, utils.Count()),
		utils.NewMeasureView(certSignFailedTotal, []tag.Key{failedReasonKey}, utils.Count()),
		utils.NewMeasureView(serverTLSCertIssueFailedTotal, []tag.Key{failedReasonKey}, utils.Count()),
		utils.NewMeasureView(issuerCertChangedTotal, noKeys, utils.Count()),
		utils.NewMeasureView(issuerCertExpiryTimestamp, noKeys, view.LastValue()),
	)
}
