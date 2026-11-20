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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests that daprd exports the mTLS init and workload certificate
// rotation metrics.
type mtls struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	// A short workload cert TTL makes the sidecar rotate its certificate a few
	// seconds after it starts.
	m.sentry = sentry.New(t,
		sentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    workloadCertTTL: "10s"
    allowedClockSkew: "5s"
`),
	)

	m.daprd = daprd.New(t, daprd.WithSentry(t, m.sentry))

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.daprd),
	}
}

func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	appID := "app_id:" + m.daprd.AppID()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.daprd.Metrics(c, ctx)
		assert.InDelta(c, 1, metrics.SumWithLabels("dapr_runtime_mtls_init_total", appID), 0)
		assert.GreaterOrEqual(c, metrics.SumWithLabels("dapr_runtime_mtls_workload_cert_rotated_total", appID), float64(1))
	}, 20*time.Second, 100*time.Millisecond)
}
