/*
Copyright 2023 The Dapr Authors
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

package monitoring

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
	caRenewalTotal = stats.Int64(
		"sentry/ca/renewal_total",
		"The number of automatic CA renewals, appending a new trust anchor and pending issuer.",
		stats.UnitDimensionless)
	caSwitchoverTotal = stats.Int64(
		"sentry/ca/switchover_total",
		"The number of CA signing key switchovers to a renewed issuer.",
		stats.UnitDimensionless)
	caRenewalPending = stats.Int64(
		"sentry/ca/renewal_pending",
		"Whether a renewed CA is pending switchover (1) or not (0).",
		stats.UnitDimensionless)
	caSwitchoverTimestamp = stats.Int64(
		"sentry/ca/switchover_timestamp",
		"The unix timestamp, in seconds, when the pending renewed issuer will be used for signing. Zero when no renewal is pending.",
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
		diagUtils.WithTags(certSignFailedTotal.Name(), failedReasonKey, reason),
		certSignFailedTotal.M(1))
}

// ServerCertIssueFailed records server cert issue failure.
func ServerCertIssueFailed(reason string) {
	stats.Record(context.Background(), serverTLSCertIssueFailedTotal.M(1))
}

// IssuerCertExpiry records root cert expiry.
func IssuerCertExpiry(expiry time.Time) {
	stats.Record(context.Background(), issuerCertExpiryTimestamp.M(expiry.Unix()))
}

// IssuerCertChanged records issuer credential change.
func IssuerCertChanged() {
	stats.Record(context.Background(), issuerCertChangedTotal.M(1))
}

// CARenewed counts automatic CA renewals.
func CARenewed() {
	stats.Record(context.Background(), caRenewalTotal.M(1))
}

// CASwitchover counts switchovers of the signing key to a renewed issuer.
func CASwitchover() {
	stats.Record(context.Background(), caSwitchoverTotal.M(1))
}

// CAPending records whether a renewed CA is pending switchover, and when the
// switchover will happen.
func CAPending(pending bool, switchAt time.Time) {
	var pendingVal, switchAtVal int64
	if pending {
		pendingVal = 1
		switchAtVal = switchAt.Unix()
	}
	stats.Record(context.Background(),
		caRenewalPending.M(pendingVal),
		caSwitchoverTimestamp.M(switchAtVal),
	)
}

// InitMetrics initializes metrics.
func InitMetrics() error {
	return view.Register(
		diagUtils.NewMeasureView(csrReceivedTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(certSignSuccessTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(certSignFailedTotal, []tag.Key{failedReasonKey}, view.Count()),
		diagUtils.NewMeasureView(serverTLSCertIssueFailedTotal, []tag.Key{failedReasonKey}, view.Count()),
		diagUtils.NewMeasureView(issuerCertChangedTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(issuerCertExpiryTimestamp, noKeys, view.LastValue()),
		diagUtils.NewMeasureView(caRenewalTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(caSwitchoverTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(caRenewalPending, noKeys, view.LastValue()),
		diagUtils.NewMeasureView(caSwitchoverTimestamp, noKeys, view.LastValue()),
	)
}
