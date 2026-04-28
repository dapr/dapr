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

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// WorkflowAccessPolicyUpdate streams workflow access policy changes to sidecars.
func (a *apiServer) WorkflowAccessPolicyUpdate(in *operatorv1pb.WorkflowAccessPolicyUpdateRequest, srv operatorv1pb.Operator_WorkflowAccessPolicyUpdateServer) error {
	if a.closed.Load() {
		return errors.New("server is closed")
	}

	log.Info("sidecar connected for workflow access policy updates")

	ctx := srv.Context()

	ch, cancel, err := a.policyInformer.WatchUpdates(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	stream, err := sender.New(srv)
	if err != nil {
		return err
	}

	cl := loopsclient.New(loopsclient.Options[wfaclapi.WorkflowAccessPolicy]{
		EventCh:     ch,
		CancelWatch: cancel,
		Stream:      stream,
		Namespace:   in.GetNamespace(),
		KubeClient:  a.Client,
	})
	defer cl.CacheLoop()

	if err := cl.Run(ctx); err != nil {
		log.Warnf("workflow access policy client loop ended with error: %s", err)
	}

	return nil
}

// ListWorkflowAccessPolicy returns the list of workflow access policies in a namespace.
func (a *apiServer) ListWorkflowAccessPolicy(ctx context.Context, in *operatorv1pb.ListWorkflowAccessPolicyRequest) (*operatorv1pb.ListWorkflowAccessPolicyResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListWorkflowAccessPolicyResponse{
		Policies: [][]byte{},
	}

	var policies wfaclapi.WorkflowAccessPolicyList
	if err := a.Client.List(ctx, &policies, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing workflow access policies: %w", err)
	}

	for _, item := range policies.Items {
		b, err := json.Marshal(item)
		if err != nil {
			log.Warnf("Error marshalling workflow access policy: %s", err)
			continue
		}
		resp.Policies = append(resp.Policies, b)
	}

	return resp, nil
}
