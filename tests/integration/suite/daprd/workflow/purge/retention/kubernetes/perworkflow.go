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

package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(perworkflow))
}

// perworkflow tests that per-workflow retention entries survive the
// operator's CRD JSON round trip to the sidecar.
type perworkflow struct{ cluster }

func (p *perworkflow) Setup(t *testing.T) []framework.Option {
	return p.setup(t, &configapi.WorkflowStateRetentionPolicy{
		AnyTerminal: &metav1.Duration{Duration: time.Hour},
		Workflows: []configapi.NamedWorkflowStateRetentionPolicy{{
			Name:      "fast",
			Completed: &metav1.Duration{Duration: time.Second},
		}},
	})
}

func (p *perworkflow) Run(t *testing.T, ctx context.Context) {
	reg := dworkflow.NewRegistry()
	for _, name := range []string{"fast", "other"} {
		reg.AddWorkflowN(name, func(*dworkflow.WorkflowContext) (any, error) {
			return nil, nil
		})
	}
	client := p.client(t, ctx, reg)

	fast, err := client.ScheduleWorkflow(ctx, "fast")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, fast)
	require.NoError(t, err)
	other, err := client.ScheduleWorkflow(ctx, "other")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, other)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.ListInstanceIDs(ctx)
		require.NoError(c, err)
		assert.Equal(c, []string{other}, resp.InstanceIds)
	}, time.Second*20, time.Millisecond*10)
}
