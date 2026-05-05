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

package operator

import (
	"context"
	"encoding/json"
	"fmt"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type workflowAccessPolicies struct {
	operatorpb.Operator_WorkflowAccessPolicyUpdateClient
}

// The go linter does not yet understand that these functions are being used by
// the generic operator.
//
//nolint:unused
func (w *workflowAccessPolicies) list(ctx context.Context, opclient operatorpb.OperatorClient, ns string) ([][]byte, error) {
	resp, err := opclient.ListWorkflowAccessPolicy(ctx, &operatorpb.ListWorkflowAccessPolicyRequest{
		Namespace: ns,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetPolicies(), nil
}

//nolint:unused
func (w *workflowAccessPolicies) close() error {
	if w.Operator_WorkflowAccessPolicyUpdateClient != nil {
		return w.CloseSend()
	}

	return nil
}

//nolint:unused
func (w *workflowAccessPolicies) recv(ctx context.Context) (*loader.Event[wfaclapi.WorkflowAccessPolicy], error) {
	event, err := w.Recv()
	if err != nil {
		return nil, err
	}

	var policy wfaclapi.WorkflowAccessPolicy
	if err := json.Unmarshal(event.GetPolicy(), &policy); err != nil {
		return nil, fmt.Errorf("failed to deserialize workflow access policy: %w", err)
	}

	return &loader.Event[wfaclapi.WorkflowAccessPolicy]{
		Resource: policy,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (w *workflowAccessPolicies) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns string) error {
	stream, err := opclient.WorkflowAccessPolicyUpdate(ctx, &operatorpb.WorkflowAccessPolicyUpdateRequest{
		Namespace: ns,
	})
	if err != nil {
		return err
	}

	w.Operator_WorkflowAccessPolicyUpdateClient = stream

	return nil
}
