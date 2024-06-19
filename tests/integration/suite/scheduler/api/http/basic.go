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

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	b.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(b.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(b.scheduler, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/job/schedule/test", b.daprd.HTTPPort())

	httpClient := util.HTTPClient(t)

	t.Run("bad json", func(t *testing.T) {
		for _, body := range []string{
			"",
			"{}",
			`["job": {}]`,
			`"job": {}`,
			`{"job"}`,
			`{"job": {}}`,
			`{"job": {"name": "test"}}`,
			`{"job": {"schedule": "test", "repeats": -1}}`,
		} {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		}
	})

	t.Run("good json", func(t *testing.T) {
		for _, body := range []string{
			`{"job": {"schedule": "@daily"}}`,
			`{"job": {"schedule": "@daily", "repeats": 3, "due_time": "10s", "ttl": "11s"}}`,
			`{"job": {"schedule": "@daily", "repeats": 3, "due_time": "10s", "ttl": "11s", "data": {"@type": "type.googleapis.com/google.protobuf.StringValue", "value": "Hello, World!"}}}`,
		} {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, string(body))
		}
	})
}
