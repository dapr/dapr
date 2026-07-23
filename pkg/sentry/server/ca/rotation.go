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
	"errors"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
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

	// Enabled turns the rotation loop on. Off by default: rotation replaces
	// the root CA with a sentry-generated self-signed one, which must never
	// happen to an operator-provided root CA.
	Enabled bool

	// TriggerWindow is how far before expiry to begin rotation.
	TriggerWindow time.Duration
	// PropagationWindow is how long to distribute dual trust anchors before
	// switching signing to the new issuer. Must be >= workload cert TTL so all
	// existing workload certs are renewed against the new trust anchors.
	PropagationWindow time.Duration
	// CheckInterval is how often the rotation loop polls cert expiry.
	CheckInterval time.Duration
}

func newRotator(s store, c *ca) *rotator {
	rcfg := rotatorConfig{
		TrustDomain:       c.config.TrustDomain,
		AllowedClockSkew:  c.config.AllowedClockSkew,
		WorkloadCertTTL:   c.config.WorkloadCertTTL,
		Enabled:           c.config.Rotation.Enabled,
		TriggerWindow:     c.config.Rotation.TriggerWindow,
		PropagationWindow: c.config.Rotation.PropagationWindow,
		CheckInterval:     c.config.Rotation.CheckInterval,
	}
	if rcfg.TriggerWindow <= 0 {
		rcfg.TriggerWindow = config.DefaultRotationTriggerWindow
	}
	if rcfg.PropagationWindow <= 0 {
		rcfg.PropagationWindow = config.DefaultRotationPropagationWindow
	}
	if rcfg.CheckInterval <= 0 {
		rcfg.CheckInterval = config.DefaultRotationCheckInterval
	}
	// Switching signing before every workload cert has been renewed against
	// the combined trust anchors would break mTLS across the mesh, so the
	// propagation window can never be shorter than the workload cert TTL.
	if rcfg.PropagationWindow < rcfg.WorkloadCertTTL {
		log.Warnf("Rotation propagation window %s is shorter than the workload cert TTL %s; using %s",
			rcfg.PropagationWindow, rcfg.WorkloadCertTTL, rcfg.WorkloadCertTTL)
		rcfg.PropagationWindow = rcfg.WorkloadCertTTL
	}

	return &rotator{
		store:  s,
		ca:     c,
		config: rcfg,
	}
}

// validateRotationState checks that a rotation state loaded from storage is
// complete and consistent for its phase. Incomplete state (e.g. from a crash
// mid-write or manual edits) must not drive the state machine: zero
// timestamps would make phase transitions appear due immediately, and empty
// pending credentials would eventually be promoted for signing.
func validateRotationState(rot *bundle.RotationState) error {
	switch rot.Phase {
	case bundle.RotationPhaseDistributing:
	case bundle.RotationPhaseSigning:
		if rot.SigningAt.IsZero() {
			return errors.New("rotation state in signing phase is missing the signing timestamp")
		}
	default:
		return fmt.Errorf("unknown rotation phase %q", rot.Phase)
	}
	if rot.DistributedAt.IsZero() {
		return errors.New("rotation state is missing the distributed timestamp")
	}
	if rot.OldRootNotAfter.IsZero() {
		return errors.New("rotation state is missing the old root CA expiry")
	}
	if len(rot.NewTrustAnchors) == 0 || len(rot.NewIssChainPEM) == 0 || len(rot.NewIssKeyPEM) == 0 {
		return errors.New("rotation state is missing pending rotation credentials")
	}
	return nil
}

// Run is the long-running rotation loop. It is registered as a concurrency
// runner alongside the gRPC server.
func (r *rotator) Run(ctx context.Context) error {
	if !r.config.Enabled {
		// Warn if a previous deployment left rotation state behind: with
		// rotation disabled it will be neither advanced nor cleaned up.
		if bndle, err := r.store.get(ctx); err == nil && bndle.Rotation != nil {
			log.Warnf("Root CA rotation is disabled but rotation state is present (phase %q); the rotation will not be advanced", bndle.Rotation.Phase)
		}
		log.Info("Automatic root CA rotation is disabled; enable with -rotation-enabled")
		<-ctx.Done()
		return nil
	}

	ticker := time.NewTicker(r.config.CheckInterval)
	defer ticker.Stop()

	// Evaluate immediately on startup so a sentry restarted mid-rotation (or
	// started close to root CA expiry) does not wait a full check interval
	// before acting.
	if err := r.tick(ctx); err != nil {
		log.Errorf("Root CA rotation error: %v", err)
	}

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

	// The store returns a nil X.509 bundle when the stored state is absent or
	// inconsistent (e.g. the trust anchors in the Kubernetes Secret and
	// ConfigMap diverged after a partial write). The state machine cannot make
	// safe decisions from that, so surface it rather than dereference nil.
	if bndle.X509 == nil {
		return errors.New("stored trust bundle is missing or inconsistent; skipping rotation check")
	}

	rot := bndle.Rotation

	if rot == nil {
		// No rotation in progress — check if we should start one.
		rootCert := bndle.X509.IssChain[len(bndle.X509.IssChain)-1]
		if time.Until(rootCert.NotAfter) < r.config.TriggerWindow {
			return r.startDistributing(ctx, bndle)
		}
		return nil
	}

	switch rot.Phase {
	case bundle.RotationPhaseDistributing:
		// Wait until the propagation window has elapsed so that all Kubernetes
		// pods have had a chance to pick up the combined trust anchors via the
		// ConfigMap volume mount before we switch signing.
		if time.Since(rot.DistributedAt) >= r.config.PropagationWindow {
			return r.switchSigning(ctx, bndle)
		}
	case bundle.RotationPhaseSigning:
		// Clean up once the old root CA has expired AND all workload certs
		// signed by the old issuer have also expired AND the combined trust
		// anchors have demonstrably reached every trust anchor consumer — a
		// time window alone cannot rule out a lagging namespace that would be
		// cut off by removing the old root.
		graceElapsed := time.Since(rot.SigningAt) > r.config.WorkloadCertTTL+r.config.AllowedClockSkew
		if time.Now().After(rot.OldRootNotAfter) && graceElapsed {
			if err := r.store.verifyPropagation(ctx, rot); err != nil {
				log.Warnf("Root CA rotation: delaying cleanup: %v", err)
				return nil
			}
			return r.cleanup(ctx, bndle)
		}
	default:
		// A silent no-op here would stall the rotation indefinitely while the
		// root CA marches towards expiry; surface it so operators see it in
		// the logs on every check.
		return fmt.Errorf("unknown rotation phase %q", rot.Phase)
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
	log.Infof("Root CA rotation: dual trust anchors distributed; will switch signing in %s", r.config.PropagationWindow)
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
