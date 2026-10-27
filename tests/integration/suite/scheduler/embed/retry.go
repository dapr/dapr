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

package embed

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retry))
}

// retry verifies the scheduler run loop survives a runtime failure of a server
// incarnation instead of exiting the process.
type retry struct {
	scheduler *scheduler.Scheduler
	logline   *logline.LogLine
	ln        net.Listener
	ln6       net.Listener
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 1)
	r.ln = fp.Listener(t)
	port := r.ln.Addr().(*net.TCPAddr).Port
	if ln6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
		r.ln6 = ln6
	}
	// Release-on-abort: if the test fails before Run reaches the deliberate
	// release below, the bound port must not leak. closeListeners is
	// idempotent (fields are nil-ed on close) so the double invocation on the
	// happy path is a no-op.
	t.Cleanup(r.closeListeners)

	r.logline = logline.New(t, logline.WithStdoutLineContains(
		"Scheduler server failed, recreating in",
	))

	r.scheduler = scheduler.New(t,
		scheduler.WithEtcdClientPort(port),
		scheduler.WithLogLineStdout(r.logline),
	)

	return []framework.Option{
		framework.WithProcesses(r.logline, r.scheduler),
	}
}

// closeListeners releases the squatted etcd client port(s). Safe to call
// more than once.
func (r *retry) closeListeners() {
	if r.ln != nil {
		r.ln.Close()
		r.ln = nil
	}
	if r.ln6 != nil {
		r.ln6.Close()
		r.ln6 = nil
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.logline.EventuallyFoundAll(t)

	httpClient := client.HTTP(t)
	healthzURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", r.scheduler.HealthzPort())
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthzURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err, "scheduler process must stay alive while etcd cannot bind")
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		time.Sleep(100 * time.Millisecond)
	}

	// Release the port: the next incarnation starts etcd and recovers.
	r.closeListeners()

	r.scheduler.WaitUntilRunning(t, ctx)

	_, err := r.scheduler.Client(t, ctx).ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test",
		Job: &schedulerv1.Job{
			Schedule: new("@every 20s"),
		},
		Metadata: &schedulerv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, r.scheduler.EtcdJobs(t, ctx), 1)
}
