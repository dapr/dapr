/*
Copyright 2023 The Dapr Authors
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

package ports

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procoperator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(operator))
}

// operator tests that the ports are available when operator is running.
type operator struct {
	proc *procoperator.Operator
}

func (o *operator) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	kubeAPI := kubernetes.New(t, kubernetes.WithBaseOperatorAPI(t,
		spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
		"dapr-system",
		sentry.Port(),
	))

	o.proc = procoperator.New(t,
		procoperator.WithNamespace("dapr-system"),
		procoperator.WithKubeconfigPath(kubeAPI.KubeconfigPath(t)),
		procoperator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(kubeAPI, sentry, o.proc),
	}
}

func (o *operator) Run(t *testing.T, ctx context.Context) {
	dialer := net.Dialer{Timeout: time.Second}
	for name, port := range map[string]int{
		"port":    o.proc.Port(),
		"metrics": o.proc.MetricsPort(),
		"healthz": o.proc.HealthzPort(),
	} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", port))
			//nolint:testifylint
			_ = assert.NoError(c, err) && assert.NoError(c, conn.Close())
		}, time.Second*10, 100*time.Millisecond, "port %s (:%d) was not available in time", name, port)
	}
}
