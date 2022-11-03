/*
Copyright 2021 The Dapr Authors
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
	"time"

	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
)

type sentryMetrics struct {
	csrReceivedTotal              syncint64.Counter
	certSignSuccessTotal          syncint64.Counter
	certSignFailedTotal           syncint64.Counter
	serverTLSCertIssueFailedTotal syncint64.Counter
	issuerCertChangedTotal        syncint64.Counter
	issuerCertExpiryTimestamp     syncint64.Counter
}

func (m *MetricClient) newSentryMetrics() *sentryMetrics {
	sm := new(sentryMetrics)
	// Metrics definitions.
	sm.csrReceivedTotal, _ = m.meter.SyncInt64().Counter(
		"sentry/cert/sign/request_received_total",
		instrument.WithDescription("The number of CSRs received."),
		instrument.WithUnit(unit.Dimensionless))
	sm.certSignSuccessTotal, _ = m.meter.SyncInt64().Counter(
		"sentry/cert/sign/success_total",
		instrument.WithDescription("The number of certificates issuances that have succeeded."),
		instrument.WithUnit(unit.Dimensionless))
	sm.certSignFailedTotal, _ = m.meter.SyncInt64().Counter(
		"sentry/cert/sign/failure_total",
		instrument.WithDescription("The number of errors occurred when signing the CSR."),
		instrument.WithUnit(unit.Dimensionless))
	sm.serverTLSCertIssueFailedTotal, _ = m.meter.SyncInt64().Counter(
		"sentry/servercert/issue_failed_total",
		instrument.WithDescription("The number of server TLS certificate issuance failures."),
		instrument.WithUnit(unit.Dimensionless))
	sm.issuerCertChangedTotal, _ = m.meter.SyncInt64().Counter(
		"sentry/issuercert/changed_total",
		instrument.WithDescription("The number of issuer cert updates, when issuer cert or key is changed"),
		instrument.WithUnit(unit.Dimensionless))
	sm.issuerCertExpiryTimestamp, _ = m.meter.SyncInt64().Counter(
		"sentry/issuercert/expiry_timestamp",
		instrument.WithDescription("The unix timestamp, in seconds, when issuer/root cert will expire."),
		instrument.WithUnit(unit.Dimensionless))

	return sm
}

// CertSignRequestReceived counts when CSR received.
func (s *sentryMetrics) CertSignRequestReceived(ctx context.Context) {
	if s == nil {
		return
	}
	s.csrReceivedTotal.Add(ctx, 1)
}

// CertSignSucceed counts succeeded cert issuance.
func (s *sentryMetrics) CertSignSucceed(ctx context.Context) {
	if s == nil {
		return
	}
	s.certSignSuccessTotal.Add(ctx, 1)
}

// CertSignFailed counts succeeded cert issuance.
func (s *sentryMetrics) CertSignFailed(ctx context.Context, reason string) {
	if s == nil {
		return
	}
	s.certSignFailedTotal.Add(ctx, 1, isemconv.FailReasonKey.String(reason))
}

// IssuerCertExpiry records root cert expiry.
func (s *sentryMetrics) IssuerCertExpiry(ctx context.Context, expiry *time.Time) {
	if s == nil {
		return
	}
	s.issuerCertExpiryTimestamp.Add(ctx, expiry.Unix())
}

// ServerCertIssueFailed records server cert issue failure.
func (s *sentryMetrics) ServerCertIssueFailed(ctx context.Context, reason string) {
	if s == nil {
		return
	}
	s.serverTLSCertIssueFailedTotal.Add(ctx, 1)
}

// IssuerCertChanged records issuer credential change.
func (s *sentryMetrics) IssuerCertChanged(ctx context.Context) {
	if s == nil {
		return
	}
	s.issuerCertChangedTotal.Add(ctx, 1)
}
