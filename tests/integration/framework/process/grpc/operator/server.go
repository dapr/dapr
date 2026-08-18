/*
Copyright 2023 The Dapr Authors
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

	"google.golang.org/protobuf/types/known/emptypb"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type server struct {
	componentUpdateFn            func(*operatorv1.ComponentUpdateRequest, operatorv1.Operator_ComponentUpdateServer) error
	configurationUpdateFn        func(*operatorv1.ConfigurationUpdateRequest, operatorv1.Operator_ConfigurationUpdateServer) error
	getConfigurationFn           func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error)
	getResiliencyFn              func(context.Context, *operatorv1.GetResiliencyRequest) (*operatorv1.GetResiliencyResponse, error)
	httpEndpointUpdateFn         func(*operatorv1.HTTPEndpointUpdateRequest, operatorv1.Operator_HTTPEndpointUpdateServer) error
	listComponentsFn             func(context.Context, *operatorv1.ListComponentsRequest) (*operatorv1.ListComponentResponse, error)
	listHTTPEndpointsFn          func(context.Context, *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error)
	listMCPServersFn             func(context.Context, *operatorv1.ListMCPServersRequest) (*operatorv1.ListMCPServersResponse, error)
	listResiliencyFn             func(context.Context, *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error)
	listSubscriptionsFn          func(context.Context, *emptypb.Empty) (*operatorv1.ListSubscriptionsResponse, error)
	listSubscriptionsV2Fn        func(context.Context, *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error)
	mcpServerUpdateFn            func(*operatorv1.MCPServerUpdateRequest, operatorv1.Operator_MCPServerUpdateServer) error
	resiliencyUpdateFn           func(*operatorv1.ResiliencyUpdateRequest, operatorv1.Operator_ResiliencyUpdateServer) error
	subscriptionUpdateFn         func(*operatorv1.SubscriptionUpdateRequest, operatorv1.Operator_SubscriptionUpdateServer) error
	listWorkflowAccessPoliciesFn func(context.Context, *operatorv1.ListWorkflowAccessPolicyRequest) (*operatorv1.ListWorkflowAccessPolicyResponse, error)
	workflowAccessPolicyUpdateFn func(*operatorv1.WorkflowAccessPolicyUpdateRequest, operatorv1.Operator_WorkflowAccessPolicyUpdateServer) error
}

func (s *server) ComponentUpdate(req *operatorv1.ComponentUpdateRequest, srv operatorv1.Operator_ComponentUpdateServer) error {
	if s.componentUpdateFn != nil {
		return s.componentUpdateFn(req, srv)
	}
	return nil
}

func (s *server) GetConfiguration(ctx context.Context, in *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
	if s.getConfigurationFn != nil {
		return s.getConfigurationFn(ctx, in)
	}
	return new(operatorv1.GetConfigurationResponse), nil
}

func (s *server) GetResiliency(ctx context.Context, in *operatorv1.GetResiliencyRequest) (*operatorv1.GetResiliencyResponse, error) {
	if s.getResiliencyFn != nil {
		return s.getResiliencyFn(ctx, in)
	}
	return nil, nil
}

func (s *server) HTTPEndpointUpdate(in *operatorv1.HTTPEndpointUpdateRequest, srv operatorv1.Operator_HTTPEndpointUpdateServer) error {
	if s.httpEndpointUpdateFn != nil {
		return s.httpEndpointUpdateFn(in, srv)
	}
	return nil
}

func (s *server) ListComponents(ctx context.Context, in *operatorv1.ListComponentsRequest) (*operatorv1.ListComponentResponse, error) {
	if s.listComponentsFn != nil {
		return s.listComponentsFn(ctx, in)
	}
	return new(operatorv1.ListComponentResponse), nil
}

func (s *server) ListHTTPEndpoints(ctx context.Context, in *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error) {
	if s.listHTTPEndpointsFn != nil {
		return s.listHTTPEndpointsFn(ctx, in)
	}
	return new(operatorv1.ListHTTPEndpointsResponse), nil
}

func (s *server) ListMCPServers(ctx context.Context, in *operatorv1.ListMCPServersRequest) (*operatorv1.ListMCPServersResponse, error) {
	if s.listMCPServersFn != nil {
		return s.listMCPServersFn(ctx, in)
	}
	return new(operatorv1.ListMCPServersResponse), nil
}

func (s *server) MCPServerUpdate(in *operatorv1.MCPServerUpdateRequest, srv operatorv1.Operator_MCPServerUpdateServer) error {
	if s.mcpServerUpdateFn != nil {
		return s.mcpServerUpdateFn(in, srv)
	}
	return nil
}

func (s *server) ListResiliency(ctx context.Context, in *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error) {
	if s.listResiliencyFn != nil {
		return s.listResiliencyFn(ctx, in)
	}
	return new(operatorv1.ListResiliencyResponse), nil
}

func (s *server) ListSubscriptions(ctx context.Context, in *emptypb.Empty) (*operatorv1.ListSubscriptionsResponse, error) {
	if s.listSubscriptionsFn != nil {
		return s.listSubscriptionsFn(ctx, in)
	}
	return new(operatorv1.ListSubscriptionsResponse), nil
}

func (s *server) ListSubscriptionsV2(ctx context.Context, in *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error) {
	if s.listSubscriptionsV2Fn != nil {
		return s.listSubscriptionsV2Fn(ctx, in)
	}
	return new(operatorv1.ListSubscriptionsResponse), nil
}

func (s *server) SubscriptionUpdate(req *operatorv1.SubscriptionUpdateRequest, srv operatorv1.Operator_SubscriptionUpdateServer) error {
	if s.subscriptionUpdateFn != nil {
		return s.subscriptionUpdateFn(req, srv)
	}
	return nil
}

func (s *server) ConfigurationUpdate(req *operatorv1.ConfigurationUpdateRequest, srv operatorv1.Operator_ConfigurationUpdateServer) error {
	if s.configurationUpdateFn != nil {
		return s.configurationUpdateFn(req, srv)
	}
	return nil
}

func (s *server) ResiliencyUpdate(req *operatorv1.ResiliencyUpdateRequest, srv operatorv1.Operator_ResiliencyUpdateServer) error {
	if s.resiliencyUpdateFn != nil {
		return s.resiliencyUpdateFn(req, srv)
	}

	return nil
}

func (s *server) ListWorkflowAccessPolicy(ctx context.Context, in *operatorv1.ListWorkflowAccessPolicyRequest) (*operatorv1.ListWorkflowAccessPolicyResponse, error) {
	if s.listWorkflowAccessPoliciesFn != nil {
		return s.listWorkflowAccessPoliciesFn(ctx, in)
	}
	return new(operatorv1.ListWorkflowAccessPolicyResponse), nil
}

func (s *server) WorkflowAccessPolicyUpdate(req *operatorv1.WorkflowAccessPolicyUpdateRequest, srv operatorv1.Operator_WorkflowAccessPolicyUpdateServer) error {
	if s.workflowAccessPolicyUpdateFn != nil {
		return s.workflowAccessPolicyUpdateFn(req, srv)
	}
	return nil
}
