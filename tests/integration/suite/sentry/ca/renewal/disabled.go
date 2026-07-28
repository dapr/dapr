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

package renewal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled tests that no renewal happens when automatic CA renewal is
// disabled, even with an issuer certificate well within the renewal
// threshold.
type disabled struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.bundle = genBundle(t, "localhost", time.Second*65)

	d.sentry = procsentry.New(t,
		procsentry.WithCABundle(d.bundle),
		procsentry.WithCARenewalEnabled(false),
	)

	return []framework.Option{
		framework.WithProcesses(d.sentry),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.sentry.WaitUntilRunning(t, ctx)

	credDir := d.sentry.CredentialsDir()

	// The issuer is well within any renewal threshold; nothing must happen.
	time.Sleep(time.Second * 8)

	anchorsPEM, err := os.ReadFile(filepath.Join(credDir, "ca.crt"))
	require.NoError(t, err)
	assert.Len(t, parseAnchors(t, anchorsPEM), 1, "no anchor must have been appended")
	assert.Equal(t, d.bundle.X509.TrustAnchors, anchorsPEM)
	assert.NoFileExists(t, filepath.Join(credDir, "issuer.next.crt"))
	assert.NoFileExists(t, filepath.Join(credDir, "issuer.next.key"))

	metrics, err := metricsSnapshot(ctx, d.sentry.MetricsPort())
	require.NoError(t, err)
	assert.Zero(t, metrics["dapr_sentry_ca_renewal_total"])
	assert.Zero(t, metrics["dapr_sentry_ca_renewal_pending"])
	assert.Zero(t, metrics["dapr_sentry_ca_switchover_total"])
	assert.Zero(t, metrics["dapr_sentry_ca_switchover_timestamp"])
}
