/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package terminate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(httpclient))
}

type httpclient struct {
	workflow *workflow.Workflow
}

func (h *httpclient) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *httpclient) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	holdCh := make(chan struct{})
	t.Cleanup(func() { close(holdCh) })
	var inAct atomic.Bool
	h.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	h.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		inAct.Store(true)
		<-holdCh
		return nil, nil
	})

	cl := h.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)

	assert.Eventually(t, inAct.Load, time.Second*20, time.Millisecond*10)

	const reason = "Operator requested termination: maintenance window"
	query := url.Values{"reason": []string{reason}}
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/terminate?%s", h.workflow.Dapr().HTTPPort(), id, query.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_TERMINATED", meta.GetRuntimeStatus().String())

	// The reason is observable through the same workflow metadata API used by
	// applications, rather than being folded into or replacing the workflow output.
	metadataURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s", h.workflow.Dapr().HTTPPort(), id)
	metadataReq, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	require.NoError(t, err)
	metadataResp, err := client.HTTP(t).Do(metadataReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, metadataResp.StatusCode)
	var workflowMetadata struct {
		Properties map[string]string `json:"properties"`
	}
	require.NoError(t, json.NewDecoder(metadataResp.Body).Decode(&workflowMetadata))
	require.NoError(t, metadataResp.Body.Close())
	assert.Equal(t, reason, workflowMetadata.Properties["dapr.workflow.termination_reason"])
}
