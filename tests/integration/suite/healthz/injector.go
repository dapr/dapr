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

package healthz

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
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
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
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
	i.injector.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)
	reqURL := fmt.Sprintf("http://localhost:%d/healthz", i.injector.HealthzPort())
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, time.Second*10, 10*time.Millisecond)
}
