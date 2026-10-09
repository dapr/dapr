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

package standalone

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mtlsInitMetric))
}

// mtlsInitMetric tests that the sidecar emits the dapr_runtime_mtls_init_total
// metric once mTLS has been initialized.
type mtlsInitMetric struct {
	daprd     *daprd.Daprd
	sentry    *sentry.Sentry
	placement *placement.Placement
	scheduler *scheduler.Scheduler
}

func (m *mtlsInitMetric) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	trustAnchors := m.sentry.CABundle().X509.TrustAnchors

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, trustAnchors, 0o600))

	m.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(m.sentry.Address()),
	)

	m.scheduler = scheduler.New(t,
		scheduler.WithSentry(m.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppID("my-app"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(trustAnchors))),
		daprd.WithSentryAddress(m.sentry.Address()),
		daprd.WithPlacementAddresses(m.placement.Address()),
		daprd.WithSchedulerAddresses(m.scheduler.Address()),
		daprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.placement, m.scheduler, m.daprd),
	}
}

func (m *mtlsInitMetric) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.placement.WaitUntilRunning(t, ctx)
	m.scheduler.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.daprd.Metrics(c, ctx).All()
		assert.GreaterOrEqual(c, int(metrics["dapr_runtime_mtls_init_total|app_id:my-app"]), 1,
			"expected the mTLS init metric to be recorded once mTLS is up")
	}, time.Second*10, time.Millisecond*10)
}
