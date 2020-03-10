package monitoring

import (
	"context"
	"time"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
	certSignSuccessTotal = stats.Int64(
		"sentry/cert/sign/success_total",
		"The number of certificates issuances that have succeeded.",
		stats.UnitDimensionless)
	certSignFailedTotal = stats.Int64(
		"sentry/cert/sign/failure_total",
		"The number of errors occurred when signing the CSR.",
		stats.UnitDimensionless)
	rootCertExpiryTimestamp = stats.Int64(
		"sentry/rootcert/expiry_timestamp",
		"The unix timestamp, in seconds, when root cert will expire.",
		stats.UnitDimensionless)
	rootCertRotateTotal = stats.Int64(
		"sentry/rootcert/rotated_total",
		"The number of root certificate rotated.",
		stats.UnitDimensionless)
	issuerCredentialChangeTotal = stats.Int64(
		"sentry/issuercert/changed_total",
		"The number of issuer cert updates, when issuer cert or key is changed",
		stats.UnitDimensionless)

	// Metrics Tags
	failedReasonKey = tag.MustNewKey("reason")
)

type certSignFailReason string

const (
	CertParseError        certSignFailReason = "cert_parse"
	CertValidationError   certSignFailReason = "cert_validation"
	ReqIDError            certSignFailReason = "req_id_validation"
	CertSignError         certSignFailReason = "cert_sign"
	InsufficientDataError certSignFailReason = "insufficient_data"
)

// CertSignRequestRecieved counts when CSR received.
func CertSignRequestRecieved() {
	stats.Record(context.Background(), csrReceivedTotal.M(1))
}

// CertSignSucceed counts succeeded cert issuance
func CertSignSucceed() {
	stats.Record(context.Background(), certSignSuccessTotal.M(1))
}

// CertSignFailed counts succeeded cert issuance
func CertSignFailed(reason certSignFailReason) {
	stats.RecordWithTags(
		context.Background(),
		diag_utils.WithTags(failedReasonKey, string(reason)),
		certSignFailedTotal.M(1))
}

// RootCertExpiry records root cert expiry
func RootCertExpiry(expiry time.Time) {
	stats.Record(context.Background(), rootCertExpiryTimestamp.M(expiry.Unix()))
}

// RootCertRotationSucceed records root cert rotation
func RootCertRotationSucceed() {
	stats.Record(context.Background(), rootCertRotateTotal.M(1))
}

// IssuerCredentialChanged records issuer credential change
func IssuerCredentialChanged() {
	stats.Record(context.Background(), issuerCredentialChangeTotal.M(1))
}

// InitMetrics initializes metrics
func InitMetrics() error {
	var nilKey = []tag.Key{}
	return view.Register(
		diag_utils.NewMeasureView(csrReceivedTotal, nilKey, view.Count()),
		diag_utils.NewMeasureView(certSignSuccessTotal, nilKey, view.Count()),
		diag_utils.NewMeasureView(certSignFailedTotal, []tag.Key{failedReasonKey}, view.Count()),
		diag_utils.NewMeasureView(rootCertRotateTotal, nilKey, view.Count()),
		diag_utils.NewMeasureView(issuerCredentialChangeTotal, nilKey, view.Count()),
		diag_utils.NewMeasureView(rootCertExpiryTimestamp, nilKey, view.LastValue()),
	)
}
