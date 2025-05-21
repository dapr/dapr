/*
Copyright 2022 The Dapr Authors
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

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)

	o.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, o.daprd),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("overwrite if exists", func(t *testing.T) {
		type request struct {
			name     string
			body     string
			assertFn func(j *runtimev1pb.Job)
		}

		for _, r := range []request{
			{"overwrite1", `{"schedule": "@daily"}`, func(j *runtimev1pb.Job) {
				assert.Equal(t, "@daily", j.GetSchedule())
				assert.Equal(t, uint32(0), j.GetRepeats())
			}},
			{"overwrite1", `{"schedule": "@weekly", "repeats": 3, "overwrite": true, "due_time": "10s", "ttl": "11s"}`, func(j *runtimev1pb.Job) {
				assert.Equal(t, "@weekly", j.GetSchedule())
				assert.Equal(t, uint32(3), j.GetRepeats())
			}},
		} {
			postURL := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/%s", o.daprd.HTTPPort(), r.name)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(r.body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, string(body))

			job, err := o.daprd.GRPCClient(t, ctx).GetJobAlpha1(ctx,
				&runtimev1pb.GetJobRequest{Name: "overwrite1"},
			)
			require.NoError(t, err)
			r.assertFn(job.GetJob())
		}
	})

	t.Run("do not overwrite if exist", func(t *testing.T) {
		type request struct {
			name       string
			body       string
			statusCode int
		}

		for _, r := range []request{
			{"overwrite2", `{"schedule": "@daily"}`, http.StatusNoContent},
			{"overwrite2", `{"schedule": "@daily", "repeats": 3, "due_time": "10s", "ttl": "11s"}`, http.StatusConflict},
			{"overwrite2", `{"schedule": "@daily", "repeats": 3, "overwrite": false, "due_time": "10s", "ttl": "11s"}`, http.StatusConflict},
		} {
			postURL := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/%s", o.daprd.HTTPPort(), r.name)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(r.body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, r.statusCode, resp.StatusCode)
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			job, err := o.daprd.GRPCClient(t, ctx).GetJobAlpha1(ctx,
				&runtimev1pb.GetJobRequest{Name: "overwrite2"},
			)
			require.NoError(t, err)
			assert.Equal(t, "@daily", job.GetJob().GetSchedule())
			assert.Equal(t, uint32(0), job.GetJob().GetRepeats())
		}
	})
}
