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
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sentry))
}

// sentry tests that Sentry responds to healthz requests.
type sentry struct {
	proc *procsentry.Sentry
}

func (s *sentry) Setup(t *testing.T) []framework.Option {
	s.proc = procsentry.New(t)
	return []framework.Option{
		framework.WithProcesses(s.proc),
	}
}

func (s *sentry) Run(t *testing.T, ctx context.Context) {
	s.proc.WaitUntilRunning(t, ctx)

	reqURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", s.proc.HealthzPort())
	httpClient := util.HTTPClient(t)
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return http.StatusOK == resp.StatusCode
	}, time.Second*10, 100*time.Millisecond)
}
