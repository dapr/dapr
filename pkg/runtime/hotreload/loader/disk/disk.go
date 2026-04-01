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

package disk

import (
	"context"
	"fmt"
	"strings"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	loaderdisk "github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/disk/dirdata"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.loader.disk")

type Options struct {
	AppID          string
	Dirs           []string
	ComponentStore *compstore.ComponentStore
}

type disk struct {
	dirs                   []string
	components             *resource[compapi.Component]
	subscriptions          *resource[subapi.Subscription]
	mcpServers             *resource[mcpserverapi.MCPServer]
	configurations         *resource[configapi.Configuration]
	httpEndpoints          *resource[httpendpointapi.HTTPEndpoint]
	resiliencies           *resource[resiliencyapi.Resiliency]
	workflowAccessPolicies *resource[wfaclapi.WorkflowAccessPolicy]
	fs                     *fswatcher.FSWatcher
}

func New(opts Options) (loader.Interface, error) {
	log.Infof("Watching directories: [%s]", strings.Join(opts.Dirs, ", "))

	fs, err := fswatcher.New(fswatcher.Options{
		Targets: opts.Dirs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	diskOpts := loaderdisk.Options{
		AppID: opts.AppID,
		Paths: opts.Dirs,
	}

	return &disk{
		dirs: opts.Dirs,
		fs:   fs,
		components: newResource[compapi.Component](
			resourceOptions[compapi.Component]{
				loader: loaderdisk.NewComponents(diskOpts),
				store:  store.NewComponents(opts.ComponentStore),
			},
		),
		subscriptions: newResource[subapi.Subscription](
			resourceOptions[subapi.Subscription]{
				loader: loaderdisk.NewSubscriptions(diskOpts),
				store:  store.NewSubscriptions(opts.ComponentStore),
			},
		),
		configurations: newResource[configapi.Configuration](
			resourceOptions[configapi.Configuration]{
				loader: loaderdisk.NewConfigurations(diskOpts),
				store:  store.NewConfigurations(opts.ComponentStore),
			},
		),
		httpEndpoints: newResource[httpendpointapi.HTTPEndpoint](
			resourceOptions[httpendpointapi.HTTPEndpoint]{
				loader: loaderdisk.NewHTTPEndpoints(diskOpts),
				store:  store.NewHTTPEndpoints(opts.ComponentStore),
			},
		),
		resiliencies: newResource[resiliencyapi.Resiliency](
			resourceOptions[resiliencyapi.Resiliency]{
				loader: loaderdisk.NewResiliencies(diskOpts),
				store:  store.NewResiliencies(opts.ComponentStore),
			},
		),
		mcpServers: newResource[mcpserverapi.MCPServer](
			resourceOptions[mcpserverapi.MCPServer]{
				loader: loaderdisk.NewMCPServers(loaderdisk.Options{
					AppID: opts.AppID,
					Paths: opts.Dirs,
				}),
				store: store.NewMCPServers(opts.ComponentStore),
			},
		),
		workflowAccessPolicies: newResource[wfaclapi.WorkflowAccessPolicy](
			resourceOptions[wfaclapi.WorkflowAccessPolicy]{
				loader: loaderdisk.NewWorkflowAccessPolicies(loaderdisk.Options{
					AppID: opts.AppID,
					Paths: opts.Dirs,
				}),
				store: store.NewWorkflowAccessPolicies(opts.ComponentStore),
			},
		),
	}, nil
}

func (d *disk) Run(ctx context.Context) error {
	eventCh := make(chan struct{})

	return concurrency.NewRunnerManager(
		d.components.run,
		d.subscriptions.run,
		d.mcpServers.run,
		d.configurations.run,
		d.httpEndpoints.run,
		d.resiliencies.run,
		d.workflowAccessPolicies.run,
		func(ctx context.Context) error {
			return d.fs.Run(ctx, eventCh)
		},
		func(ctx context.Context) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-eventCh:
					// Read all YAML files from all directories once, then pass
					// the pre-read data to each resource loader. This avoids
					// each resource type independently opening and reading the
					// same files, which causes file handle contention on
					// Windows.
					dirData, err := dirdata.ReadDirs(d.dirs)
					if err != nil {
						return fmt.Errorf("failed to read resource directories: %w", err)
					}
					if err := d.components.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.subscriptions.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.mcpServers.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.configurations.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.httpEndpoints.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.resiliencies.trigger(ctx, dirData); err != nil {
						return err
					}
					if err := d.workflowAccessPolicies.trigger(ctx); err != nil {
						return err
					}
				}
			}
		},
	).Run(ctx)
}

func (d *disk) Components() loader.Loader[compapi.Component] {
	return d.components
}

func (d *disk) Subscriptions() loader.Loader[subapi.Subscription] {
	return d.subscriptions
}

func (d *disk) MCPServers() loader.Loader[mcpserverapi.MCPServer] {
	return d.mcpServers
}

func (d *disk) Configurations() loader.Loader[configapi.Configuration] {
	return d.configurations
}

func (d *disk) HTTPEndpoints() loader.Loader[httpendpointapi.HTTPEndpoint] {
	return d.httpEndpoints
}

func (d *disk) Resiliencies() loader.Loader[resiliencyapi.Resiliency] {
	return d.resiliencies
}

func (d *disk) WorkflowAccessPolicies() loader.Loader[wfaclapi.WorkflowAccessPolicy] {
	return d.workflowAccessPolicies
}
