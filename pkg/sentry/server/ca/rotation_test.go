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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

// mockRotationStore is a minimal in-memory store for testing the rotator.
type mockRotationStore struct {
	bndle    bundle.Bundle
	stored   *bundle.Bundle
	getErr   error
	storeErr error
}

func (m *mockRotationStore) get(_ context.Context) (bundle.Bundle, error) {
	return m.bndle, m.getErr
}

func (m *mockRotationStore) store(_ context.Context, b bundle.Bundle) error {
	cp := b
	m.stored = &cp
	return m.storeErr
}

// genEd25519Key returns a fresh Ed25519 private key.
func genEd25519Key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

// makeTestX509Bundle generates a bundle whose root CA and issuer cert expire at notAfter.
// Requires NAMESPACE env var to be set (use t.Setenv before calling).
func makeTestX509Bundle(t *testing.T, notAfter time.Time) bundle.Bundle {
	t.Helper()
	ttl := time.Until(notAfter)
	x509b, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      genEd25519Key(t),
		TrustDomain:      "test.example.com",
		AllowedClockSkew: 0,
		OverrideCATTL:    &ttl,
	})
	require.NoError(t, err)
	return bundle.Bundle{X509: x509b}
}

// newTestRotator builds a rotator with controlled config for testing.
func newTestRotator(ms *mockRotationStore, caObj *ca, cfg rotatorConfig) *rotator {
	r := newRotator(ms, caObj)
	r.config = cfg
	return r
}

func TestRotatorTick(t *testing.T) {
	// NAMESPACE is required by bundle.GenerateX509 → generateIssuerCert → security.CurrentNamespace()
	t.Setenv("NAMESPACE", "test-ns")

	cfg := rotatorConfig{
		TrustDomain:      "test.example.com",
		AllowedClockSkew: 0,
		WorkloadCertTTL:  24 * time.Hour,
	}

	t.Run("no rotation in progress, cert far from expiry, nothing happens", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(60*24*time.Hour))
		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		assert.Nil(t, ms.stored, "store should not be written when cert is far from expiry")
	})

	t.Run("no rotation in progress, cert near expiry, starts distributing phase", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(15*24*time.Hour))

		// Capture issuer cert expiry before tick can mutate the shared *X509 pointer.
		originalIssuerExpiry := bndle.X509.IssChain[0].NotAfter
		originalAnchorLen := len(bndle.X509.TrustAnchors)

		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		require.NotNil(t, ms.stored, "store must be written when cert is near expiry")

		stored := ms.stored
		require.NotNil(t, stored.Rotation, "rotation state must be set")
		assert.Equal(t, bundle.RotationPhaseDistributing, stored.Rotation.Phase)
		assert.NotEmpty(t, stored.Rotation.NewTrustAnchors, "new trust anchors must be generated")
		assert.NotEmpty(t, stored.Rotation.NewIssChainPEM, "new issuer chain PEM must be generated")
		assert.NotEmpty(t, stored.Rotation.NewIssKeyPEM, "new issuer key PEM must be generated")
		assert.NotNil(t, stored.Rotation.NewIssChain, "new issuer chain must be parsed")
		assert.NotNil(t, stored.Rotation.NewIssKey, "new issuer key must be parsed")
		assert.False(t, stored.Rotation.DistributedAt.IsZero(), "DistributedAt must be set")
		assert.Equal(t, originalIssuerExpiry, stored.Rotation.OldRootNotAfter,
			"OldRootNotAfter should equal the expiry of the last cert in the issuer chain")

		// Combined trust anchors must be longer than the original (old + new root CAs).
		assert.Greater(t, len(stored.X509.TrustAnchors), originalAnchorLen,
			"trust anchors should contain both old and new root CAs")

		// In-memory CA trust anchors must be hot-swapped immediately.
		caObj.mu.RLock()
		inMemAnchors := caObj.bundle.X509.TrustAnchors
		caObj.mu.RUnlock()
		assert.Equal(t, stored.X509.TrustAnchors, inMemAnchors,
			"in-memory trust anchors must be updated to the combined set")
	})

	t.Run("distributing phase, propagation window not yet elapsed, nothing happens", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(15*24*time.Hour))
		bndle.Rotation = &bundle.RotationState{
			Phase:         bundle.RotationPhaseDistributing,
			DistributedAt: time.Now(), // just distributed — window not elapsed
		}
		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		assert.Nil(t, ms.stored, "store must not be written while propagation window is open")
	})

	t.Run("distributing phase, propagation window elapsed, switches to signing", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(15*24*time.Hour))

		// Generate the pending new X.509 bundle that would have been created in startDistributing.
		newTTL := 365 * 24 * time.Hour
		newX509, err := bundle.GenerateX509(bundle.OptionsX509{
			X509RootKey:      genEd25519Key(t),
			TrustDomain:      "test.example.com",
			AllowedClockSkew: 0,
			OverrideCATTL:    &newTTL,
		})
		require.NoError(t, err)

		// Simulate what startDistributing would have stored.
		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseDistributing,
			DistributedAt:   time.Now().Add(-25 * time.Hour), // propagation window (24h) has elapsed
			NewTrustAnchors: newX509.TrustAnchors,
			NewIssChainPEM:  newX509.IssChainPEM,
			NewIssKeyPEM:    newX509.IssKeyPEM,
			NewIssChain:     newX509.IssChain,
			NewIssKey:       newX509.IssKey,
		}

		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err = r.tick(t.Context())
		require.NoError(t, err)
		require.NotNil(t, ms.stored, "store must be written when switching to signing")

		stored := ms.stored
		require.NotNil(t, stored.Rotation, "rotation state must still be set")
		assert.Equal(t, bundle.RotationPhaseSigning, stored.Rotation.Phase)
		assert.False(t, stored.Rotation.SigningAt.IsZero(), "SigningAt must be recorded")

		// Active signing bundle must now use the new issuer cert.
		assert.Equal(t, newX509.IssChainPEM, stored.X509.IssChainPEM,
			"signing must use new issuer cert chain")
		assert.Equal(t, newX509.IssKeyPEM, stored.X509.IssKeyPEM,
			"signing must use new issuer key")

		// In-memory bundle must be updated.
		caObj.mu.RLock()
		inMemBundle := caObj.bundle.X509
		caObj.mu.RUnlock()
		assert.Equal(t, newX509.IssChainPEM, inMemBundle.IssChainPEM,
			"in-memory issuer chain must be updated")
		assert.Equal(t, newX509.IssKeyPEM, inMemBundle.IssKeyPEM,
			"in-memory issuer key must be updated")
	})

	t.Run("signing phase, old root not yet expired, no cleanup", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))
		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseSigning,
			SigningAt:       time.Now().Add(-2 * time.Hour),
			OldRootNotAfter: time.Now().Add(1 * time.Hour), // old root still valid
		}
		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		assert.Nil(t, ms.stored, "store must not be written while old root CA is still valid")
	})

	t.Run("signing phase, old root expired but grace period not elapsed, no cleanup", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))
		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseSigning,
			SigningAt:       time.Now().Add(-2 * time.Hour), // only 2h ago; WorkloadCertTTL is 24h
			OldRootNotAfter: time.Now().Add(-1 * time.Hour), // old root expired
		}
		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		assert.Nil(t, ms.stored, "store must not be written while grace period (WorkloadCertTTL) has not elapsed")
	})

	t.Run("signing phase, old root expired and grace period elapsed, cleanup runs", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))

		// The new trust anchors that should be the only ones remaining after cleanup.
		newTrustAnchors := bndle.X509.TrustAnchors

		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseSigning,
			SigningAt:       time.Now().Add(-26 * time.Hour), // WorkloadCertTTL (24h) + skew elapsed
			OldRootNotAfter: time.Now().Add(-2 * time.Hour),  // old root CA has expired
			NewTrustAnchors: newTrustAnchors,
		}
		ms := &mockRotationStore{bndle: bndle}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.NoError(t, err)
		require.NotNil(t, ms.stored, "store must be written during cleanup")

		stored := ms.stored
		assert.Nil(t, stored.Rotation, "rotation state must be cleared after cleanup")
		assert.Equal(t, newTrustAnchors, stored.X509.TrustAnchors,
			"only the new root CA should remain in trust anchors after cleanup")

		// In-memory trust anchors must reflect only the new root CA.
		caObj.mu.RLock()
		inMemAnchors := caObj.bundle.X509.TrustAnchors
		caObj.mu.RUnlock()
		assert.Equal(t, newTrustAnchors, inMemAnchors,
			"in-memory trust anchors must be updated to only the new root CA")
	})
}

func TestRotatorTickStoreErrors(t *testing.T) {
	t.Setenv("NAMESPACE", "test-ns")

	cfg := rotatorConfig{
		TrustDomain:      "test.example.com",
		AllowedClockSkew: 0,
		WorkloadCertTTL:  24 * time.Hour,
	}

	t.Run("get error is propagated", func(t *testing.T) {
		ms := &mockRotationStore{getErr: assert.AnError}
		caObj := &ca{}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to load bundle for rotation check")
	})

	t.Run("store error during startDistributing is propagated", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(10*24*time.Hour)) // near expiry
		ms := &mockRotationStore{bndle: bndle, storeErr: assert.AnError}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to store rotation state")
	})

	t.Run("store error during switchSigning is propagated", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(15*24*time.Hour))

		newTTL := 365 * 24 * time.Hour
		newX509, err := bundle.GenerateX509(bundle.OptionsX509{
			X509RootKey:      genEd25519Key(t),
			TrustDomain:      "test.example.com",
			AllowedClockSkew: 0,
			OverrideCATTL:    &newTTL,
		})
		require.NoError(t, err)

		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseDistributing,
			DistributedAt:   time.Now().Add(-25 * time.Hour),
			NewTrustAnchors: newX509.TrustAnchors,
			NewIssChainPEM:  newX509.IssChainPEM,
			NewIssKeyPEM:    newX509.IssKeyPEM,
			NewIssChain:     newX509.IssChain,
			NewIssKey:       newX509.IssKey,
		}

		ms := &mockRotationStore{bndle: bndle, storeErr: assert.AnError}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err = r.tick(t.Context())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to store new signing bundle")
	})

	t.Run("store error during cleanup is propagated", func(t *testing.T) {
		bndle := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))
		bndle.Rotation = &bundle.RotationState{
			Phase:           bundle.RotationPhaseSigning,
			SigningAt:       time.Now().Add(-26 * time.Hour),
			OldRootNotAfter: time.Now().Add(-2 * time.Hour),
			NewTrustAnchors: bndle.X509.TrustAnchors,
		}

		ms := &mockRotationStore{bndle: bndle, storeErr: assert.AnError}
		caObj := &ca{bundle: bndle}
		r := newTestRotator(ms, caObj, cfg)

		err := r.tick(t.Context())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to store cleaned-up bundle")
	})
}
