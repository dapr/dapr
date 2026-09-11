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

package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	sentryutils "github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(cleanupblocked))
}

// cleanupblocked ensures the destructive cleanup phase is gated on evidence
// of propagation: a namespace whose operator-synced trust bundle ConfigMap
// has not received the new root CA defers cleanup — even after the old root
// has expired and the time window has elapsed — until the ConfigMap is
// synced, so that namespace is never cut off from the mesh.
type cleanupblocked struct {
	sentry *procsentry.Sentry
	bndl   bundle.Bundle
	tb     sentryutils.TrustBundleRW
}

func (e *cleanupblocked) Setup(t *testing.T) []framework.Option {
	// Same full-cycle timings as the cleanup scenario, plus an extra
	// namespace whose trust bundle ConfigMap is never updated by anything —
	// simulating an operator that has not (yet) synced it.
	e.bndl = cert.GenerateCABundle(t, trustDomain, time.Second*20)

	kubeAPI, tb := sentryutils.KubeAPIRW(t, sentryutils.KubeAPIOptions{
		Bundle:                     e.bndl,
		Namespace:                  "mynamespace",
		ServiceAccount:             "myserviceaccount",
		AppID:                      "myappid",
		WorkloadCertTTL:            "3s",
		AllowedClockSkew:           "1s",
		ExtraTrustBundleNamespaces: []string{"lagging"},
	})
	e.tb = tb
	e.sentry = procsentry.New(t,
		procsentry.WithKubeAPI(t, kubeAPI, "sentrynamespace"),
		procsentry.WithCABundle(e.bndl),
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
		procsentry.WithRotationPropagationWindow(time.Second*3),
	)

	return []framework.Option{framework.WithProcesses(kubeAPI, e.sentry)}
}

func (e *cleanupblocked) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := e.tb.Secret.Current(t)
		assert.Equal(c, string(bundle.RotationPhaseSigning), string(secret.Data[procsentry.RotationPhaseSecretKey]))
	}, time.Second*15, time.Millisecond*10)

	secret := e.tb.Secret.Current(t)
	oldRootNotAfter, err := time.Parse(time.RFC3339, string(secret.Data[procsentry.RotationOldRootNotAfterSecretKey]))
	require.NoError(t, err)

	// Through the entire window in which cleanup would otherwise fire — old
	// root expiry plus the workload cert grace period plus several check
	// intervals — the rotation must stay parked in the signing phase, because
	// the lagging namespace has not received the new root CA.
	window := time.Until(oldRootNotAfter) + time.Second*8
	assert.Never(t, func() bool {
		_, ok := e.tb.Secret.Current(t).Data[procsentry.RotationPhaseSecretKey]
		return !ok
	}, window, time.Millisecond*10, "cleanup must be deferred while a namespace is lagging")

	// Sync the lagging namespace's ConfigMap, as the operator would.
	laggingCM := e.tb.ExtraConfigMaps["lagging"].Current(t)
	laggingCM.Data["ca.crt"] = e.tb.ConfigMap.Current(t).Data["ca.crt"]
	e.tb.ExtraConfigMaps["lagging"].Set(t, laggingCM)

	// With every namespace carrying the new root, cleanup now proceeds.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		currentSecret := e.tb.Secret.Current(t)
		_, ok := currentSecret.Data[procsentry.RotationPhaseSecretKey]
		if !assert.False(c, ok, "rotation state must be cleared once the lagging namespace has synced") {
			return
		}
		assert.Len(c, cert.DecodePEM(t, currentSecret.Data["ca.crt"]), 1,
			"only the new root CA must remain in the trust anchors")
	}, time.Second*30, time.Millisecond*10)
}
