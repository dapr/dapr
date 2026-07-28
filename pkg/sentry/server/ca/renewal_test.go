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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

func genX509(t *testing.T, ttl time.Duration) *bundle.X509 {
	t.Helper()
	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	x, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:   rootKey,
		TrustDomain:   "test.example.com",
		OverrideCATTL: &ttl,
	})
	require.NoError(t, err)
	return x
}

func renewX509(t *testing.T, existing *bundle.X509, ttl time.Duration) *bundle.X509 {
	t.Helper()
	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	renewed, err := bundle.RenewX509(bundle.OptionsRenewX509{
		Existing:      existing,
		X509RootKey:   rootKey,
		TrustDomain:   "test.example.com",
		OverrideCATTL: &ttl,
	})
	require.NoError(t, err)
	return renewed
}

func TestRenewalStateMachine(t *testing.T) {
	t.Parallel()

	conf := config.Config{
		AllowedClockSkew:            0,
		TrustAnchorPropagationGrace: time.Hour,
		CARenewalThreshold:          0.9,
	}

	t.Run("no pending pair is Normal", func(t *testing.T) {
		t.Parallel()
		x := genX509(t, time.Hour*100)
		assert.Equal(t, stateNormal, deriveState(x, time.Now(), conf))
	})

	t.Run("pending pair before grace is Pending, after grace is DueForSwitch", func(t *testing.T) {
		t.Parallel()
		x := genX509(t, time.Hour*100)
		renewed := renewX509(t, x, time.Hour*100)

		genTime := renewed.NextIssChain[0].NotBefore
		assert.Equal(t, statePending, deriveState(renewed, genTime.Add(time.Minute), conf))
		assert.Equal(t, statePending, deriveState(renewed, genTime.Add(time.Hour-time.Minute), conf))
		assert.Equal(t, stateDueForSwitch, deriveState(renewed, genTime.Add(time.Hour+time.Minute), conf))
	})

	t.Run("switch time is clamped to the active issuer expiry", func(t *testing.T) {
		t.Parallel()
		// Active issuer expires in 30m: even with a 1h grace, the switch must
		// happen before the active issuer expires.
		x := genX509(t, time.Minute*30)
		renewed := renewX509(t, x, time.Hour*100)

		expiry := renewed.IssChain[0].NotAfter
		st := switchTime(renewed, 0, conf.TrustAnchorPropagationGrace)
		assert.False(t, st.After(expiry), "switch time %s must not be after active issuer expiry %s", st, expiry)
		assert.Equal(t, stateDueForSwitch, deriveState(renewed, expiry.Add(time.Second), conf), "must switch no later than the active issuer expiry, before the grace has elapsed")
	})

	t.Run("renewDue triggers only past the lifetime fraction and not while pending", func(t *testing.T) {
		t.Parallel()
		x := genX509(t, time.Hour*100)
		notBefore := x.IssChain[0].NotBefore

		// Threshold 0.9 of a 100h lifetime: renewal is due 90h in.
		assert.False(t, renewDue(x, notBefore.Add(time.Hour*89), 0.9))
		assert.True(t, renewDue(x, notBefore.Add(time.Hour*91), 0.9))
		assert.True(t, renewDue(x, x.IssChain[0].NotAfter.Add(time.Hour), 0.9))

		renewed := renewX509(t, x, time.Hour*100)
		assert.False(t, renewDue(renewed, notBefore.Add(time.Hour*91), 0.9), "must not renew while pending")
	})

	t.Run("promote swaps the pending pair in and clears it", func(t *testing.T) {
		t.Parallel()
		x := genX509(t, time.Hour*100)
		renewed := renewX509(t, x, time.Hour*100)
		nextChainPEM := renewed.NextIssChainPEM
		nextKeyPEM := renewed.NextIssKeyPEM
		anchors := renewed.TrustAnchors

		promote(renewed)

		assert.Equal(t, nextChainPEM, renewed.IssChainPEM)
		assert.Equal(t, nextKeyPEM, renewed.IssKeyPEM)
		assert.Nil(t, renewed.NextIssChainPEM)
		assert.Nil(t, renewed.NextIssKey)
		assert.Equal(t, anchors, renewed.TrustAnchors, "trust anchors are append only")
	})

	t.Run("orphan anchor detection", func(t *testing.T) {
		t.Parallel()
		x := genX509(t, time.Hour*100)
		now := time.Now()

		orphan, err := hasOrphanAnchor(x, now, 0.9)
		require.NoError(t, err)
		assert.False(t, orphan, "single anchor backing the chain is not an orphan")

		// Append a foreign long-lived anchor without a pending pair.
		foreign := genX509(t, time.Hour*100)
		x.TrustAnchors = append(x.TrustAnchors, foreign.TrustAnchors...)
		orphan, err = hasOrphanAnchor(x, now, 0.9)
		require.NoError(t, err)
		assert.True(t, orphan)

		// A pending pair explains the extra anchor.
		fresh := genX509(t, time.Hour*100)
		renewed := renewX509(t, fresh, time.Hour*100)
		orphan, err = hasOrphanAnchor(renewed, now, 0.9)
		require.NoError(t, err)
		assert.False(t, orphan)

		// A foreign anchor already past its own renewal point is inert, not
		// an orphan.
		fresh2 := genX509(t, time.Hour*100)
		expired := genX509(t, time.Minute)
		fresh2.TrustAnchors = append(fresh2.TrustAnchors, expired.TrustAnchors...)
		orphan, err = hasOrphanAnchor(fresh2, now.Add(time.Hour), 0.9)
		require.NoError(t, err)
		assert.False(t, orphan)
	})
}

func newSelfhostedStore(t *testing.T) *selfhosted {
	t.Helper()
	dir := t.TempDir()
	return &selfhosted{config: config.Config{
		RootCertPath:       filepath.Join(dir, "ca.crt"),
		IssuerCertPath:     filepath.Join(dir, "issuer.crt"),
		IssuerKeyPath:      filepath.Join(dir, "issuer.key"),
		NextIssuerCertPath: filepath.Join(dir, "issuer.next.crt"),
		NextIssuerKeyPath:  filepath.Join(dir, "issuer.next.key"),
	}}
}

func TestNewFromStoreRenewal(t *testing.T) {
	t.Parallel()

	newConf := func(s *selfhosted) config.Config {
		conf := s.config
		conf.TrustDomain = "test.example.com"
		conf.CARenewalEnabled = true
		conf.CATTL = time.Hour * 100
		conf.CARenewalThreshold = 0.9
		conf.TrustAnchorPropagationGrace = time.Hour
		return conf
	}

	t.Run("startup mid pending stays pending and does not re-renew", func(t *testing.T) {
		t.Parallel()
		s := newSelfhostedStore(t)
		conf := newConf(s)

		x := genX509(t, time.Hour*100)
		renewed := renewX509(t, x, time.Hour*100)
		require.NoError(t, s.store(t.Context(), bundle.Bundle{X509: renewed}))

		cl := clocktesting.NewFakeClock(renewed.NextIssChain[0].NotBefore.Add(time.Minute))
		signer, err := newFromStore(t.Context(), conf, s, cl)
		require.NoError(t, err)

		got, err := s.get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, renewed.IssChainPEM, got.X509.IssChainPEM, "still signing with the old issuer")
		assert.Equal(t, renewed.NextIssChainPEM, got.X509.NextIssChainPEM, "pending pair still stored")
		assert.Equal(t, renewed.TrustAnchors, signer.TrustAnchors())
	})

	t.Run("startup after grace promotes exactly once and persists", func(t *testing.T) {
		t.Parallel()
		s := newSelfhostedStore(t)
		conf := newConf(s)

		x := genX509(t, time.Hour*100)
		renewed := renewX509(t, x, time.Hour*100)
		require.NoError(t, s.store(t.Context(), bundle.Bundle{X509: renewed}))

		cl := clocktesting.NewFakeClock(renewed.NextIssChain[0].NotBefore.Add(time.Hour * 2))
		signer, err := newFromStore(t.Context(), conf, s, cl)
		require.NoError(t, err)

		got, err := s.get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, renewed.NextIssChainPEM, got.X509.IssChainPEM, "signing with the renewed issuer")
		assert.Nil(t, got.X509.NextIssChainPEM, "pending pair removed")
		assert.Equal(t, renewed.TrustAnchors, got.X509.TrustAnchors, "anchors untouched")
		assert.Equal(t, renewed.TrustAnchors, signer.TrustAnchors())

		// Reloading again is a no-op.
		signer2, err := newFromStore(t.Context(), conf, s, cl)
		require.NoError(t, err)
		got2, err := s.get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, got.X509.IssChainPEM, got2.X509.IssChainPEM)
		assert.Equal(t, signer.TrustAnchors(), signer2.TrustAnchors())
	})
}

func TestRunRenewal(t *testing.T) {
	t.Parallel()

	t.Run("renews when the threshold elapses and returns nil to reload", func(t *testing.T) {
		t.Parallel()
		s := newSelfhostedStore(t)
		conf := s.config
		conf.TrustDomain = "test.example.com"
		conf.CARenewalEnabled = true
		conf.CATTL = time.Hour * 100
		conf.CARenewalThreshold = 0.9
		conf.TrustAnchorPropagationGrace = time.Hour

		x := genX509(t, time.Hour*100)
		require.NoError(t, s.store(t.Context(), bundle.Bundle{X509: x}))

		cl := clocktesting.NewFakeClock(x.IssChain[0].NotBefore.Add(time.Minute))
		signer, err := newFromStore(t.Context(), conf, s, cl)
		require.NoError(t, err)

		runDone := make(chan error)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			runDone <- signer.Run(ctx)
		}()

		// Not due yet: Run should be waiting on its timer.
		require.Eventually(t, cl.HasWaiters, time.Second*5, time.Millisecond*10)

		// Cross the renewal threshold: 0.9 of the 100h lifetime.
		cl.SetTime(x.IssChain[0].NotBefore.Add(time.Hour * 91))

		select {
		case rerr := <-runDone:
			require.NoError(t, rerr)
		case <-time.After(time.Second * 10):
			t.Fatal("timed out waiting for renewal run to return")
		}

		got, err := s.get(t.Context())
		require.NoError(t, err)
		assert.NotNil(t, got.X509.NextIssChainPEM, "pending pair persisted")
		assert.Equal(t, x.IssChainPEM, got.X509.IssChainPEM, "active issuer unchanged")
		assert.Greater(t, len(got.X509.TrustAnchors), len(x.TrustAnchors), "anchor appended")
	})

	t.Run("disabled renewal blocks until context cancelled", func(t *testing.T) {
		t.Parallel()
		s := newSelfhostedStore(t)
		conf := s.config
		conf.TrustDomain = "test.example.com"

		x := genX509(t, time.Hour)
		require.NoError(t, s.store(t.Context(), bundle.Bundle{X509: x}))

		signer, err := newFromStore(t.Context(), conf, s, clocktesting.NewFakeClock(x.IssChain[0].NotAfter.Add(-time.Minute)))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		runDone := make(chan error)
		go func() {
			runDone <- signer.Run(ctx)
		}()

		select {
		case <-runDone:
			t.Fatal("run should not have returned")
		case <-time.After(time.Millisecond * 100):
		}
		cancel()
		select {
		case rerr := <-runDone:
			require.NoError(t, rerr)
		case <-time.After(time.Second * 5):
			t.Fatal("timed out waiting for run to stop")
		}

		got, err := s.get(t.Context())
		require.NoError(t, err)
		assert.Nil(t, got.X509.NextIssChainPEM, "no renewal must have happened")
	})
}
