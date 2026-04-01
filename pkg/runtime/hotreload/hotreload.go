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

package hotreload

import (
	"context"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/healthz"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/operator"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload")

type OptionsReloaderDisk struct {
	Config         *config.Configuration
	AppID          string
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Healthz        healthz.Healthz
}

type OptionsReloaderOperator struct {
	Namespace      string
	Client         operatorv1.OperatorClient
	Config         *config.Configuration
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Healthz        healthz.Healthz
}

type Reloader struct {
	isEnabled               bool
	loader                  loader.Interface
	componentsReconciler    *reconciler.Reconciler[compapi.Component]
	subscriptionsReconciler *reconciler.Reconciler[subapi.Subscription]
	mcpServersReconciler    *reconciler.Reconciler[mcpserverapi.MCPServer]

	// SIGHUP reconcilers for resources that require runtime restart
	configurationsReconciler *reconciler.SIGHUPReconciler[configapi.Configuration]
	httpEndpointsReconciler  *reconciler.SIGHUPReconciler[httpendpointapi.HTTPEndpoint]
	resilienciesReconciler   *reconciler.SIGHUPReconciler[resiliencyapi.Resiliency]

	// policyOptsCh receives options for the WorkflowAccessPolicy reconciler.
	// SetPolicyRecompiler sends on this channel; Run() receives from it. This
	// synchronizes the race between initRuntime() and Run().
	policyOptsCh chan reconciler.WorkflowAccessPolicyOptions
}

func NewDisk(opts OptionsReloaderDisk) (*Reloader, error) {
	isEnabled := opts.Config.IsFeatureEnabled(config.HotReload)
	if !isEnabled {
		return &Reloader{isEnabled: false}, nil
	}

	loader, err := disk.New(disk.Options{
		AppID:          opts.AppID,
		Dirs:           opts.Dirs,
		ComponentStore: opts.ComponentStore,
	})
	if err != nil {
		return nil, err
	}

	return &Reloader{
		isEnabled: isEnabled,
		loader:    loader,
		componentsReconciler: reconciler.NewComponents(reconciler.Options[compapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		subscriptionsReconciler: reconciler.NewSubscriptions(reconciler.Options[subapi.Subscription]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		mcpServersReconciler: reconciler.NewMCPServers(reconciler.Options[mcpserverapi.MCPServer]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		configurationsReconciler: reconciler.NewSIGHUPConfigurations(reconciler.Options[configapi.Configuration]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		httpEndpointsReconciler: reconciler.NewSIGHUPHTTPEndpoints(reconciler.Options[httpendpointapi.HTTPEndpoint]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		resilienciesReconciler: reconciler.NewSIGHUPResiliencies(reconciler.Options[resiliencyapi.Resiliency]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		policyOptsCh: make(chan reconciler.WorkflowAccessPolicyOptions, 1),
	}, nil
}

func NewOperator(opts OptionsReloaderOperator) *Reloader {
	isEnabled := opts.Config.IsFeatureEnabled(config.HotReload)
	if !isEnabled {
		return &Reloader{isEnabled: false}
	}

	loader := operator.New(operator.Options{
		Namespace:      opts.Namespace,
		ComponentStore: opts.ComponentStore,
		OperatorClient: opts.Client,
	})

	return &Reloader{
		isEnabled: isEnabled,
		loader:    loader,
		componentsReconciler: reconciler.NewComponents(reconciler.Options[compapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		subscriptionsReconciler: reconciler.NewSubscriptions(reconciler.Options[subapi.Subscription]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		mcpServersReconciler: reconciler.NewMCPServers(reconciler.Options[mcpserverapi.MCPServer]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		configurationsReconciler: reconciler.NewSIGHUPConfigurations(reconciler.Options[configapi.Configuration]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		httpEndpointsReconciler: reconciler.NewSIGHUPHTTPEndpoints(reconciler.Options[httpendpointapi.HTTPEndpoint]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		resilienciesReconciler: reconciler.NewSIGHUPResiliencies(reconciler.Options[resiliencyapi.Resiliency]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Healthz:   opts.Healthz,
		}),
		policyOptsCh: make(chan reconciler.WorkflowAccessPolicyOptions, 1),
	}
}

// Loader returns the underlying loader.Interface used by this reloader.
func (r *Reloader) Loader() loader.Interface {
	return r.loader
}

// SetPolicyRecompiler sends options for the WorkflowAccessPolicy reconciler.
// Run() waits for this signal before starting the reconciler. This is safe
// to call concurrently with Run() from initRuntime().
func (r *Reloader) SetPolicyRecompiler(opts reconciler.WorkflowAccessPolicyOptions) {
	if !r.isEnabled || r.policyOptsCh == nil {
		return
	}

	r.policyOptsCh <- opts
}

// SignalNoPolicyRecompiler signals Run() that no policy reconciler will be
// created. Must be called if SetPolicyRecompiler is not going to be called,
// so Run() does not block waiting.
func (r *Reloader) SignalNoPolicyRecompiler() {
	if r.isEnabled && r.policyOptsCh != nil {
		close(r.policyOptsCh)
	}
}

func (r *Reloader) Run(ctx context.Context) error {
	if !r.isEnabled {
		log.Debug("Hot reloading disabled")
		<-ctx.Done()

		return nil
	}

	log.Info("Hot reloading enabled. Daprd will reload 'Component', 'Subscription', 'MCPServer', 'Configuration', 'HTTPEndpoint' and 'Resiliency' resources when they are added, updated or deleted.")

	runners := []concurrency.Runner{
		r.loader.Run,
		r.componentsReconciler.Run,
		r.subscriptionsReconciler.Run,
		r.mcpServersReconciler.Run,
		r.configurationsReconciler.Run,
		r.httpEndpointsReconciler.Run,
		r.resilienciesReconciler.Run,
	}

	// Wait for initRuntime to signal whether a policy reconciler is needed. This
	// synchronizes with the concurrent call to SetPolicyRecompiler or
	// SignalNoPolicyRecompiler from initRuntime.
	var policyReconciler *reconciler.Reconciler[wfaclapi.WorkflowAccessPolicy]
	select {
	case opts, ok := <-r.policyOptsCh:
		if ok {
			policyReconciler = reconciler.NewWorkflowAccessPolicies(opts)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	if policyReconciler != nil {
		runners = append(runners, policyReconciler.Run)
		log.Info("Hot reloading enabled. Daprd will reload 'Component', 'Subscription', 'MCPServer', 'Configuration', 'HTTPEndpoint', 'Resiliency' and 'WorkflowAccessPolicy' resources when they are added, updated or deleted.")
	} else {
		log.Info("Hot reloading enabled. Daprd will reload 'Component', 'Subscription', 'MCPServer', 'Configuration', 'HTTPEndpoint' and 'Resiliency' resources when they are added, updated or deleted.")
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}
