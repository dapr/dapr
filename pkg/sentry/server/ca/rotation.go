/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/sentry/monitoring"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

const (
	// rotationTriggerWindow is how far before expiry to begin rotation.
	rotationTriggerWindow = 30 * 24 * time.Hour

	// propagationWindow is how long to distribute dual trust anchors before
	// switching signing to the new issuer. Must be >= workload cert TTL so all
	// existing workload certs are renewed against the new trust anchors.
	propagationWindow = 24 * time.Hour

	// rotationCheckInterval is how often the rotation loop polls cert expiry.
	rotationCheckInterval = 1 * time.Hour
)

// rotator runs the root CA rotation state machine inside the sentry process.
// It holds a pointer to the live ca so it can hot-swap the active bundle
// without a restart.
type rotator struct {
	store  store
	ca     *ca
	config rotatorConfig
}

type rotatorConfig struct {
	TrustDomain      string
	AllowedClockSkew time.Duration
	WorkloadCertTTL  time.Duration
}

func newRotator(s store, c *ca) *rotator {
	return &rotator{
		store: s,
		ca:    c,
		config: rotatorConfig{
			TrustDomain:      c.config.TrustDomain,
			AllowedClockSkew: c.config.AllowedClockSkew,
			WorkloadCertTTL:  c.config.WorkloadCertTTL,
		},
	}
}

// Run is the long-running rotation loop. It is registered as a concurrency
// runner alongside the gRPC server.
func (r *rotator) Run(ctx context.Context) error {
	ticker := time.NewTicker(rotationCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.tick(ctx); err != nil {
				log.Errorf("Root CA rotation error: %v", err)
			}
		}
	}
}

// tick evaluates the current rotation state and advances the state machine.
func (r *rotator) tick(ctx context.Context) error {
	bndle, err := r.store.get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load bundle for rotation check: %w", err)
	}

	rot := bndle.Rotation

	if rot == nil {
		// No rotation in progress — check if we should start one.
		rootCert := bndle.X509.IssChain[len(bndle.X509.IssChain)-1]
		if time.Until(rootCert.NotAfter) < rotationTriggerWindow {
			return r.startDistributing(ctx, bndle)
		}
		return nil
	}

	switch rot.Phase {
	case bundle.RotationPhaseDistributing:
		// Wait until the propagation window has elapsed so that all Kubernetes
		// pods have had a chance to pick up the combined trust anchors via the
		// ConfigMap volume mount before we switch signing.
		if time.Since(rot.DistributedAt) >= propagationWindow {
			return r.switchSigning(ctx, bndle)
		}
	case bundle.RotationPhaseSigning:
		// Clean up once the old root CA has expired AND all workload certs
		// signed by the old issuer have also expired.
		graceElapsed := time.Since(rot.SigningAt) > r.config.WorkloadCertTTL+r.config.AllowedClockSkew
		if time.Now().After(rot.OldRootNotAfter) && graceElapsed {
			return r.cleanup(ctx, bndle)
		}
	}

	return nil
}

// startDistributing generates a new root CA + issuer pair, combines both root
// CAs into the trust anchors, and persists the rotation state. After this
// phase, the ConfigMap carries both old and new root CA PEMs so all pods can
// start trusting both before signing is switched.
func (r *rotator) startDistributing(ctx context.Context, bndle bundle.Bundle) error {
	log.Info("Root CA rotation: starting DISTRIBUTING phase — generating new root CA")

	_, newRootKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate new root key: %w", err)
	}

	newX509, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      newRootKey,
		TrustDomain:      r.config.TrustDomain,
		AllowedClockSkew: r.config.AllowedClockSkew,
	})
	if err != nil {
		return fmt.Errorf("failed to generate new X.509 bundle: %w", err)
	}

	oldRootNotAfter := bndle.X509.IssChain[len(bndle.X509.IssChain)-1].NotAfter

	bndle.Rotation = &bundle.RotationState{
		Phase:           bundle.RotationPhaseDistributing,
		NewTrustAnchors: newX509.TrustAnchors,
		NewIssChainPEM:  newX509.IssChainPEM,
		NewIssKeyPEM:    newX509.IssKeyPEM,
		NewIssChain:     newX509.IssChain,
		NewIssKey:       newX509.IssKey,
		DistributedAt:   time.Now(),
		OldRootNotAfter: oldRootNotAfter,
	}

	// Combine old + new root CAs so all trust anchor consumers accept both.
	bndle.X509.TrustAnchors = append(bndle.X509.TrustAnchors, newX509.TrustAnchors...)

	if err = r.store.store(ctx, bndle); err != nil {
		return fmt.Errorf("failed to store rotation state: %w", err)
	}

	// Hot-swap the in-memory trust anchors immediately so sentry serves the
	// combined set to new CSR responses right away.
	r.ca.mu.Lock()
	r.ca.bundle.X509.TrustAnchors = bndle.X509.TrustAnchors
	r.ca.mu.Unlock()

	monitoring.RootCARotationPhaseChanged(string(bundle.RotationPhaseDistributing))
	log.Infof("Root CA rotation: dual trust anchors distributed; will switch signing in %s", propagationWindow)
	return nil
}

// switchSigning promotes the pending bundle to active: signing now uses the
// new issuer cert. Both root CAs are still in the trust anchors.
func (r *rotator) switchSigning(ctx context.Context, bndle bundle.Bundle) error {
	log.Info("Root CA rotation: switching to SIGNING phase — new issuer cert active")

	rot := bndle.Rotation
	combinedAnchors := bndle.X509.TrustAnchors // already combined old+new

	bndle.X509 = &bundle.X509{
		TrustAnchors: combinedAnchors,
		IssChainPEM:  rot.NewIssChainPEM,
		IssKeyPEM:    rot.NewIssKeyPEM,
		IssChain:     rot.NewIssChain,
		IssKey:       rot.NewIssKey,
	}
	rot.Phase = bundle.RotationPhaseSigning
	rot.SigningAt = time.Now()
	bndle.Rotation = rot

	if err := r.store.store(ctx, bndle); err != nil {
		return fmt.Errorf("failed to store new signing bundle: %w", err)
	}

	r.ca.mu.Lock()
	r.ca.bundle.X509 = bndle.X509
	r.ca.mu.Unlock()

	monitoring.RootCARotationPhaseChanged(string(bundle.RotationPhaseSigning))
	monitoring.IssuerCertChanged()
	monitoring.IssuerCertExpiry(bndle.X509.IssChain[0].NotAfter)
	log.Info("Root CA rotation: signing with new issuer cert")
	return nil
}

// cleanup removes the old root CA from trust anchors now that all workload
// certs signed by the old issuer have expired.
func (r *rotator) cleanup(ctx context.Context, bndle bundle.Bundle) error {
	log.Info("Root CA rotation: CLEANUP phase — removing old root CA from trust anchors")

	// Only the new root CA remains in trust anchors.
	bndle.X509.TrustAnchors = bndle.Rotation.NewTrustAnchors
	bndle.Rotation = nil

	if err := r.store.store(ctx, bndle); err != nil {
		return fmt.Errorf("failed to store cleaned-up bundle: %w", err)
	}

	r.ca.mu.Lock()
	r.ca.bundle.X509.TrustAnchors = bndle.X509.TrustAnchors
	r.ca.mu.Unlock()

	monitoring.RootCARotationPhaseChanged("complete")
	log.Info("Root CA rotation: complete — old root CA removed")
	return nil
}
