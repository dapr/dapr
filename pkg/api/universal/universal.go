/*
Copyright 2022 The Dapr Authors
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

// Package universal contains the implementation of APIs that are shared between gRPC and HTTP servers.
// On HTTP servers, they use protojson to convert data to/from JSON.
package universal

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/timers"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID                       string
	Namespace                   string
	Logger                      logger.Logger
	Resiliency                  resiliency.Provider
	CompStore                   *compstore.ComponentStore
	ShutdownFn                  func()
	GetComponentsCapabilitiesFn func() map[string][]string
	ExtendedMetadata            map[string]string
	AppConnectionConfig         config.AppConnectionConfig
	GlobalConfig                *config.Configuration
	Scheduler                   schedclient.Interface
	Actors                      actors.Interface
	WorkflowEngine              wfengine.Interface
}

// Universal contains the implementation of gRPC APIs that are also used by the HTTP server.
type Universal struct {
	appID                       string
	namespace                   string
	logger                      logger.Logger
	resiliency                  resiliency.Provider
	compStore                   *compstore.ComponentStore
	shutdownFn                  func()
	getComponentsCapabilitiesFn func() map[string][]string
	extendedMetadata            map[string]string
	appConnectionConfig         config.AppConnectionConfig
	globalConfig                *config.Configuration
	workflowEngine              wfengine.Interface
	scheduler                   schedclient.Interface

	extendedMetadataLock sync.RWMutex
	actors               actors.Interface
}

func New(opts Options) *Universal {
	return &Universal{
		appID:                       opts.AppID,
		namespace:                   opts.Namespace,
		logger:                      opts.Logger,
		resiliency:                  opts.Resiliency,
		compStore:                   opts.CompStore,
		shutdownFn:                  opts.ShutdownFn,
		getComponentsCapabilitiesFn: opts.GetComponentsCapabilitiesFn,
		extendedMetadata:            opts.ExtendedMetadata,
		appConnectionConfig:         opts.AppConnectionConfig,
		globalConfig:                opts.GlobalConfig,
		scheduler:                   opts.Scheduler,
		actors:                      opts.Actors,
		workflowEngine:              opts.WorkflowEngine,
	}
}

func (a *Universal) AppID() string {
	return a.appID
}

func (a *Universal) Namespace() string {
	return a.namespace
}

func (a *Universal) Resiliency() resiliency.Provider {
	return a.resiliency
}

func (a *Universal) CompStore() *compstore.ComponentStore {
	return a.compStore
}

func (a *Universal) AppConnectionConfig() config.AppConnectionConfig {
	return a.appConnectionConfig
}

func (a *Universal) ActorRouter(ctx context.Context) (router.Interface, error) {
	if err := a.actors.WaitForRegisteredHosts(ctx); err != nil {
		return nil, err
	}
	return a.actors.Router(ctx)
}

func (a *Universal) ActorState(ctx context.Context) (state.Interface, error) {
	if err := a.actors.WaitForRegisteredHosts(ctx); err != nil {
		return nil, err
	}
	return a.actors.State(ctx)
}

func (a *Universal) ActorTimers(ctx context.Context) (timers.Interface, error) {
	if err := a.actors.WaitForRegisteredHosts(ctx); err != nil {
		return nil, err
	}
	return a.actors.Timers(ctx)
}

func (a *Universal) ActorReminders(ctx context.Context) (reminders.Interface, error) {
	if err := a.actors.WaitForRegisteredHosts(ctx); err != nil {
		return nil, err
	}
	return a.actors.Reminders(ctx)
}
