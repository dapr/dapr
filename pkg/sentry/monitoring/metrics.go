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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	csrReceivedTotal              metric.Int64Counter
	certSignSuccessTotal          metric.Int64Counter
	certSignFailedTotal           metric.Int64Counter
	serverTLSCertIssueFailedTotal metric.Int64Counter
	issuerCertChangedTotal        metric.Int64Counter
	issuerCertExpiryTimestamp     metric.Int64Counter

	// Metrics Tags.
	failedReasonKey = "reason"
)

// CertSignRequestReceived counts when CSR received.
func CertSignRequestReceived() {
	csrReceivedTotal.Add(context.Background(), 1)
}

// CertSignSucceed counts succeeded cert issuance.
func CertSignSucceed() {
	certSignSuccessTotal.Add(context.Background(), 1)
}

// CertSignFailed counts succeeded cert issuance.
func CertSignFailed(reason string) {
	certSignFailedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(failedReasonKey, reason)))
}

// ServerCertIssueFailed records server cert issue failure.
func ServerCertIssueFailed(reason string) {
	serverTLSCertIssueFailedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String(failedReasonKey, reason)))
}

// IssuerCertExpiry records root cert expiry.
func IssuerCertExpiry(expiry time.Time) {
	issuerCertExpiryTimestamp.Add(context.Background(), expiry.Unix())
}

// IssuerCertChanged records issuer credential change.
func IssuerCertChanged() {
	issuerCertChangedTotal.Add(context.Background(), 1)
}

// InitMetrics initializes metrics.
func InitMetrics() error {
	m := otel.Meter("sentry")

	var err error
	csrReceivedTotal, err = m.Int64Counter(
		"sentry.cert.sign.request_received_total",
		metric.WithDescription("The number of CSRs received."),
	)
	if err != nil {
		return err
	}

	certSignSuccessTotal, err = m.Int64Counter(
		"sentry.cert.sign.success_total",
		metric.WithDescription("The number of certificates issuances that have succeeded."),
	)
	if err != nil {
		return err
	}

	certSignFailedTotal, err = m.Int64Counter(
		"sentry.cert.sign.failure_total",
		metric.WithDescription("The number of errors occurred when signing the CSR."),
	)
	if err != nil {
		return err
	}

	serverTLSCertIssueFailedTotal, err = m.Int64Counter(
		"sentry.servercert.issue_failed_total",
		metric.WithDescription("The number of server TLS certificate issuance failures."),
	)
	if err != nil {
		return err
	}

	issuerCertChangedTotal, err = m.Int64Counter(
		"sentry.issuercert.changed_total",
		metric.WithDescription("The number of issuer cert updates, when issuer cert or key is changed"),
	)
	if err != nil {
		return err
	}

	issuerCertExpiryTimestamp, err = m.Int64Counter(
		"sentry.issuercert.expiry_timestamp",
		metric.WithDescription("The unix timestamp, in seconds, when issuer/root cert will expire."),
	)
	if err != nil {
		return err
	}

	return nil
}
