/*
Copyright 2025 The Dapr Authors
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

package loadbalance

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
)

func newClusteredDeployment(t *testing.T, daprds int) *workflow.Workflow {
	wopts := []workflow.Option{
		workflow.WithDaprds(daprds),
	}
	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`
	// use the same appID for all daprds so they represent a cluster of daprds
	uid, err := uuid.NewRandom()
	require.NoError(t, err)
	appID := uid.String()

	for i := range daprds {
		wopts = append(wopts, workflow.WithDaprdOptions(i, daprd.WithAppID(appID), daprd.WithConfigManifests(t, config)))
	}
	return workflow.New(t, wopts...)
}
