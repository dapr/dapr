/*
Copyright 2026 The Dapr Authors
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

package ca

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/kit/crypto/pem"
)

// renewalState is the CA renewal state, derived purely from the stored bundle
// and the current time so it survives restarts without extra bookkeeping.
type renewalState int

const (
	// stateNormal: no pending issuer; sign with the active issuer.
	stateNormal renewalState = iota
	// statePending: a renewed trust anchor has been appended but the
	// propagation grace has not elapsed; keep signing with the old issuer.
	statePending
	// stateDueForSwitch: the grace has elapsed; promote the pending issuer.
	stateDueForSwitch
)

// switchTime returns when the pending issuer becomes the signing issuer: the
// propagation grace after the renewed certificates were generated, clamped so
// signing never continues past the active issuer's expiry.
func switchTime(x *bundle.X509, skew, grace time.Duration) time.Time {
	// NotBefore is backdated by the allowed clock skew at generation, so add
	// it back to approximate the generation time.
	switchAt := x.NextIssChain[0].NotBefore.Add(skew).Add(grace)
	if latest := x.IssChain[0].NotAfter.Add(-skew); switchAt.After(latest) {
		return latest
	}
	return switchAt
}

func deriveState(x *bundle.X509, now time.Time, conf config.Config) renewalState {
	if len(x.NextIssChain) == 0 {
		return stateNormal
	}
	if now.Before(switchTime(x, conf.AllowedClockSkew, conf.TrustAnchorPropagationGrace)) {
		return statePending
	}
	return stateDueForSwitch
}

// renewTime returns the point in the certificate's lifetime, as a fraction
// given by threshold, after which renewal is due.
func renewTime(cert *x509.Certificate, threshold float64) time.Time {
	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	return cert.NotBefore.Add(time.Duration(float64(lifetime) * threshold))
}

// renewDue reports whether the active issuer has passed the threshold
// fraction of its lifetime, with no renewal pending yet.
func renewDue(x *bundle.X509, now time.Time, threshold float64) bool {
	if len(x.NextIssChain) > 0 {
		return false
	}
	return !now.Before(renewTime(x.IssChain[0], threshold))
}

// hasOrphanAnchor reports whether the trust anchors contain a long-lived
// anchor which does not back the active issuer chain while no renewal is
// pending. This indicates a torn renewal or a half-reverted manual edit;
// automatically renewing again would append anchors without bound.
func hasOrphanAnchor(x *bundle.X509, now time.Time, threshold float64) (bool, error) {
	if len(x.NextIssChain) > 0 {
		return false, nil
	}

	anchors, err := pem.DecodePEMCertificates(x.TrustAnchors)
	if err != nil {
		return false, err
	}

	// The top-most certificate in the issuer chain is the one signed directly
	// by a trust anchor. A foreign anchor which has not yet passed its own
	// renewal point is unexplained and blocks automatic renewal.
	top := x.IssChain[len(x.IssChain)-1]
	for _, anchor := range anchors {
		if top.CheckSignatureFrom(anchor) == nil {
			continue
		}
		if now.Before(renewTime(anchor, threshold)) {
			return true, nil
		}
	}

	return false, nil
}

// promote makes the pending issuer the active signing issuer. The trust
// anchors are untouched: they are append only.
func promote(x *bundle.X509) {
	x.IssChainPEM = x.NextIssChainPEM
	x.IssKeyPEM = x.NextIssKeyPEM
	x.IssChain = x.NextIssChain
	x.IssKey = x.NextIssKey
	x.NextIssChainPEM = nil
	x.NextIssKeyPEM = nil
	x.NextIssChain = nil
	x.NextIssKey = nil
}

// renew generates a fresh root and issuer pair, appends the new trust anchor
// to the bundle and persists it. The active issuer keeps signing until the
// propagation grace elapses.
func (c *ca) renew(ctx context.Context) error {
	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate Ed25519 key for renewed CA: %w", err)
	}

	renewed, err := bundle.RenewX509(bundle.OptionsRenewX509{
		Existing:         c.bundle.X509,
		X509RootKey:      rootKey,
		TrustDomain:      c.config.TrustDomain,
		AllowedClockSkew: c.config.AllowedClockSkew,
		OverrideCATTL:    caTTL(c.config),
	})
	if err != nil {
		return fmt.Errorf("failed to renew CA bundle: %w", err)
	}

	bndle := c.bundle
	bndle.X509 = renewed
	if err := c.store.store(ctx, bndle); err != nil {
		return fmt.Errorf("failed to store renewed CA bundle: %w", err)
	}

	monitoring.CARenewed()
	log.Infof("Appended renewed trust anchor (new root expires at %s); continuing to sign with the existing issuer until %s",
		renewed.NextIssChain[0].NotAfter.Format(time.RFC3339),
		switchTime(renewed, c.config.AllowedClockSkew, c.config.TrustAnchorPropagationGrace).Format(time.RFC3339),
	)

	return nil
}

// Run runs the automatic CA renewal loop. All state transitions are persisted
// to the store and then Run returns nil, which causes the CA server to be
// restarted and the CA reloaded from the store; the reload path (newFromStore)
// is the single place state is derived and promoted, making transitions
// idempotent across crashes and restarts.
func (c *ca) Run(ctx context.Context) error {
	if !c.config.CARenewalEnabled {
		log.Info("Automatic CA renewal is disabled")
		<-ctx.Done()
		return nil
	}

	for {
		now := c.clock.Now()
		x509Bundle := c.bundle.X509

		var deadline time.Time
		switch deriveState(x509Bundle, now, c.config) {
		case stateNormal:
			orphan, err := hasOrphanAnchor(x509Bundle, now, c.config.CARenewalThreshold)
			if err != nil {
				return err
			}
			if orphan {
				log.Errorf("Trust anchors contain an unexpired anchor which does not back the active issuer chain, but no pending issuer exists. Refusing automatic CA renewal to avoid appending trust anchors without bound. Remove the orphan anchor from the trust bundle, or restore the pending issuer credentials, to re-enable automatic renewal")
				<-ctx.Done()
				return nil
			}

			if renewDue(x509Bundle, now, c.config.CARenewalThreshold) {
				log.Infof("CA issuer certificate has passed %.0f%% of its lifetime (expires at %s); renewing CA",
					c.config.CARenewalThreshold*100, x509Bundle.IssChain[0].NotAfter.Format(time.RFC3339))
				if err := c.renew(ctx); err != nil {
					return err
				}
				// Return nil to reload the CA from the store.
				return nil
			}

			deadline = renewTime(x509Bundle.IssChain[0], c.config.CARenewalThreshold)

		case statePending:
			deadline = switchTime(x509Bundle, c.config.AllowedClockSkew, c.config.TrustAnchorPropagationGrace)

		case stateDueForSwitch:
			// Return nil to reload the CA from the store; the reload promotes
			// the pending issuer and persists it.
			return nil
		}

		timer := c.clock.NewTimer(deadline.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C():
		}
	}
}
