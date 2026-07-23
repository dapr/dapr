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

package selfhosted

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nottriggered))
}

// nottriggered ensures a root CA far from expiry does not start a rotation.
type nottriggered struct {
	sentry *procsentry.Sentry
}

func (n *nottriggered) Setup(t *testing.T) []framework.Option {
	// The self-generated root CA is valid for a year, far outside the default
	// 30 day trigger window.
	n.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
	)

	return []framework.Option{framework.WithProcesses(n.sentry)}
}

func (n *nottriggered) Run(t *testing.T, ctx context.Context) {
	n.sentry.WaitUntilRunning(t, ctx)
	dir := n.sentry.BundleDirectory()

	assert.Never(t, func() bool {
		_, ok := n.sentry.RotationState()
		return ok
	}, time.Second*3, time.Millisecond*10, "rotation must not start for a root CA far from expiry")

	anchors := cert.DecodePEMFile(t, filepath.Join(dir, "ca.crt"))
	assert.Len(t, anchors, 1, "trust anchors must contain only the original root CA")

	leaf, chain, respAnchors := n.sentry.SignWorkloadCert(t, ctx, n.sentry.DiskTrustAnchorsPEM(t))
	require.NotEmpty(t, chain)
	require.Len(t, respAnchors, 1)
	assert.True(t, anchors[0].Equal(respAnchors[0]))
	require.NoError(t, leaf.CheckSignatureFrom(chain[0]))

	metrics, err := n.sentry.Metrics(t, ctx)
	require.NoError(t, err)
	assert.NotContains(t, metrics, "dapr_sentry_rootcert_rotation_total",
		"no rotation phase transition must be recorded")
}
