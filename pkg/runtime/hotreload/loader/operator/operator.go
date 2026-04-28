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
	"errors"
	"sync/atomic"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.loader.operator")

type Options struct {
	Namespace      string
	ComponentStore *compstore.ComponentStore
	OperatorClient operatorpb.OperatorClient
}

type operator struct {
	components             *resource[componentsapi.Component]
	subscriptions          *resource[subapi.Subscription]
	mcpServers             *resource[mcpserverapi.MCPServer]
	configurations         *resource[configapi.Configuration]
	httpEndpoints          *resource[httpendpointapi.HTTPEndpoint]
	resiliencies           *resource[resiliencyapi.Resiliency]
	workflowAccessPolicies *resource[wfaclapi.WorkflowAccessPolicy]

	running atomic.Bool
}

func New(opts Options) loader.Interface {
	return &operator{
		components:             newResource[componentsapi.Component](opts, loadercompstore.NewComponents(opts.ComponentStore), new(components)),
		subscriptions:          newResource[subapi.Subscription](opts, loadercompstore.NewSubscriptions(opts.ComponentStore), new(subscriptions)),
		mcpServers:             newResource[mcpserverapi.MCPServer](opts, loadercompstore.NewMCPServers(opts.ComponentStore), new(mcpservers)),
		configurations:         newResource[configapi.Configuration](opts, loadercompstore.NewConfigurations(opts.ComponentStore), new(configurations)),
		httpEndpoints:          newResource[httpendpointapi.HTTPEndpoint](opts, loadercompstore.NewHTTPEndpoints(opts.ComponentStore), new(httpEndpoints)),
		resiliencies:           newResource[resiliencyapi.Resiliency](opts, loadercompstore.NewResiliencies(opts.ComponentStore), new(resiliencies)),
		workflowAccessPolicies: newResource[wfaclapi.WorkflowAccessPolicy](opts, loadercompstore.NewWorkflowAccessPolicies(opts.ComponentStore), new(workflowAccessPolicies)),
	}
}

func (o *operator) Run(ctx context.Context) error {
	if !o.running.CompareAndSwap(false, true) {
		return errors.New("already running")
	}

	runners := make([]concurrency.Runner, 0, 6)
	for _, r := range []func() error{
		o.components.close,
		o.subscriptions.close,
		o.mcpServers.close,
		o.configurations.close,
		o.httpEndpoints.close,
		o.resiliencies.close,
		o.workflowAccessPolicies.close,
	} {
		cr := r
		runners = append(runners, func(ctx context.Context) error {
			<-ctx.Done()
			return cr()
		})
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (o *operator) Components() loader.Loader[componentsapi.Component] {
	return o.components
}

func (o *operator) Subscriptions() loader.Loader[subapi.Subscription] {
	return o.subscriptions
}

func (o *operator) MCPServers() loader.Loader[mcpserverapi.MCPServer] {
	return o.mcpServers
}

func (o *operator) Configurations() loader.Loader[configapi.Configuration] {
	return o.configurations
}

func (o *operator) HTTPEndpoints() loader.Loader[httpendpointapi.HTTPEndpoint] {
	return o.httpEndpoints
}

func (o *operator) Resiliencies() loader.Loader[resiliencyapi.Resiliency] {
	return o.resiliencies
}

func (o *operator) WorkflowAccessPolicies() loader.Loader[wfaclapi.WorkflowAccessPolicy] {
	return o.workflowAccessPolicies
}
