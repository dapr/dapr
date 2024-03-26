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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(injector))
}

type injector struct {
	injector *procinjector.Injector
}

func (i *injector) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithNamespace("dapr-system"),
		procsentry.WithTrustDomain("integration.test.dapr.io"),
	)

	i.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, i.injector),
	}
}

func (i *injector) Run(t *testing.T, ctx context.Context) {
	dialer := net.Dialer{Timeout: time.Second}
	for name, port := range map[string]int{
		"port":    i.injector.Port(),
		"metrics": i.injector.MetricsPort(),
		"healthz": i.injector.HealthzPort(),
	} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", port))
			//nolint:testifylint
			_ = assert.NoError(c, err) && assert.NoError(c, conn.Close())
		}, time.Second*10, 100*time.Millisecond, "port %s (:%d) was not available in time", name, port)
	}
}
