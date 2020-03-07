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
		"sentry/csr/received_total",
		"The number of CSRs received by Dapr Sentry server.",
		stats.UnitDimensionless)
	certSignSuccessTotal = stats.Int64(
		"sentry/cert/sign/success_total",
		"The number of certificates issuances that have succeeded.",
		stats.UnitDimensionless)
	certSignErrorTotal = stats.Int64(
		"sentry/cert/sign/error_total",
		"The number of errors occurred when signing the CSR.",
		stats.UnitDimensionless)
	rootCertExpiryTimestamp = stats.Int64(
		"sentry/rootcert/expiry_timestamp",
		"The unix timestamp, in seconds, when root cert will expire.",
		stats.UnitDimensionless)
	rootCertRotateTotal = stats.Int64(
		"sentry/rootcert/rotation_total",
		"The number of root certificate rotated.",
		stats.UnitDimensionless)
	issuerCredentialChangeTotal = stats.Int64(
		"sentry/issuer/cert/change_total",
		"The number of issuer cert updates, when issuer cert or key is changed",
		stats.UnitDimensionless)

	nilKey = []tag.Key{}

	// Metrics Tags
	errorTypeTag tag.Key
)

type certSignErrorType string

const (
	CertParseError        certSignErrorType = "CertParse"
	CertValidationError   certSignErrorType = "CertValidation"
	ReqIDError            certSignErrorType = "ReqIDValidation"
	CertSignError         certSignErrorType = "CertSign"
	InsufficientDataError certSignErrorType = "InsufficientData"
)

var log = logger.NewLogger("dapr.sentry.monitoring")

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

// CertSignSuceeed counts suceeded cert issuance
func CertSignSuceeed() {
	stats.Record(context.Background(), certSignSuccessTotal.M(1))
}

// CertSignFailed counts suceeded cert issuance
func CertSignFailed(failure certSignErrorType) {
	ctx, err := tag.New(context.Background(), tag.Insert(errorTypeTag, string(failure)))
	if err != nil {
		log.Warnf("error creating monitoring context: %v", err)
		return
	}

	stats.Record(ctx, certSignErrorTotal.M(1))
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
	var err error
	if errorTypeTag, err = tag.NewKey("error_type"); err != nil {
		return fmt.Errorf("failed to create new key: %v", err)
	}

	err = view.Register(
		newView(csrReceivedTotal, nilKey, view.Count()),
		newView(certSignSuccessTotal, nilKey, view.Count()),
		newView(certSignErrorTotal, nilKey, view.Count()),
		newView(rootCertRotateTotal, nilKey, view.Count()),
		newView(issuerCredentialChangeTotal, nilKey, view.Count()),
		newView(rootCertExpiryTimestamp, nilKey, view.LastValue()),
	)

	return err
}
