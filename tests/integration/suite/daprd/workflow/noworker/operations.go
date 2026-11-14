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

package noworker

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	frameworkclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(operations))
}

// operations asserts that every workflow operation reports that no workflow
// worker is connected, rather than an actor routing error or blocking.
type operations struct {
	workflow *workflow.Workflow
}

func (o *operations) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *operations) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	gclient := o.workflow.GRPCClient(t, ctx)
	dtclient := o.workflow.ManagementClient(t, ctx)
	sidecar := protos.NewTaskHubSidecarServiceClient(o.workflow.Dapr().GRPCConn(t, ctx))
	httpClient := frameworkclient.HTTP(t)

	ops := map[string]func() error{
		"grpc start": func() error {
			_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{WorkflowName: "foo"})
			return err
		},
		"grpc raise event": func() error {
			_, err := gclient.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{InstanceId: "abc", EventName: "bar"})
			return err
		},
		"grpc terminate": func() error {
			_, err := gclient.TerminateWorkflowBeta1(ctx, &rtv1.TerminateWorkflowRequest{InstanceId: "abc"})
			return err
		},
		"http start": func() error {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/foo/start", o.workflow.Dapr().HTTPPort())
			req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, reqURL, nil)
			if err != nil {
				return err
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			return fmt.Errorf("status %d: %s", resp.StatusCode, body)
		},
		"durabletask schedule": func() error {
			_, err := dtclient.ScheduleNewWorkflow(ctx, "foo")
			return err
		},
		"durabletask raise event": func() error {
			return dtclient.RaiseEvent(ctx, api.InstanceID("abc"), "bar")
		},
		"durabletask terminate": func() error {
			return dtclient.TerminateWorkflow(ctx, api.InstanceID("abc"))
		},
		// The durabletask client retries waits indefinitely, so call the
		// sidecar directly to observe the error it returns.
		"sidecar wait for start": func() error {
			_, err := sidecar.WaitForInstanceStart(ctx, &protos.GetInstanceRequest{InstanceId: "abc"})
			return err
		},
	}

	// Each operation waits out the actor not found retries, so run them
	// concurrently.
	var wg sync.WaitGroup
	var lock sync.Mutex
	errs := make(map[string]error, len(ops))
	for name, op := range ops {
		wg.Go(func() {
			err := op()
			lock.Lock()
			errs[name] = err
			lock.Unlock()
		})
	}
	wg.Wait()

	expMsg := "no workflow worker is connected for app '" + o.workflow.Dapr().AppID() + "'"
	for name, err := range errs {
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), expMsg, name)
	}
}
