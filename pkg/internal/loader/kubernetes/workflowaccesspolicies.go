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
	"encoding/json"
	"fmt"

	grpcretry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type workflowAccessPolicies struct {
	client    operatorv1pb.OperatorClient
	namespace string
	appID     string
}

// NewWorkflowAccessPolicies returns a new Kubernetes loader for
// WorkflowAccessPolicy resources.
func NewWorkflowAccessPolicies(opts Options, appID string) loader.Loader[wfaclapi.WorkflowAccessPolicy] {
	return &workflowAccessPolicies{
		client:    opts.Client,
		namespace: opts.Namespace,
		appID:     appID,
	}
}

// Load fetches WorkflowAccessPolicy resources from the Operator for this
// namespace, filtered to those scoped to this app.
func (w *workflowAccessPolicies) Load(ctx context.Context) ([]wfaclapi.WorkflowAccessPolicy, error) {
	resp, err := w.client.ListWorkflowAccessPolicy(ctx, &operatorv1pb.ListWorkflowAccessPolicyRequest{
		Namespace: w.namespace,
	}, grpcretry.WithMax(operatorMaxRetries), grpcretry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		log.Errorf("Error listing workflow access policies: %v", err)
		return nil, err
	}

	items := resp.GetPolicies()
	if len(items) == 0 {
		return nil, nil
	}

	policies := make([]wfaclapi.WorkflowAccessPolicy, 0, len(items))
	for _, raw := range items {
		var p wfaclapi.WorkflowAccessPolicy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("error deserializing workflow access policy: %w", err)
		}
		if !p.IsAppScoped(w.appID) {
			continue
		}
		policies = append(policies, p)
	}

	return policies, nil
}
