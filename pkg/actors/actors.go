/*
Copyright 2021 The Dapr Authors
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

package actors

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine"
	"github.com/dapr/dapr/pkg/actors/hostconfig"
	"github.com/dapr/dapr/pkg/actors/internal/apilevel"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage/scheduler"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage/statestore"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/internal/timers/inmemory"
	"github.com/dapr/dapr/pkg/actors/reminders"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app"
	"github.com/dapr/dapr/pkg/actors/timers"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.actor")

type Options struct {
	AppID              string
	Namespace          string
	Port               int
	PlacementAddresses []string
	SchedulerReminders bool
	HealthEndpoint     string
	Resiliency         resiliency.Provider
	Security           security.Handler
	SchedulerClients   *clients.Clients
	Healthz            healthz.Healthz
	CompStore          *compstore.ComponentStore
	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool
}

type InitOptions struct {
	StateStoreName string
	Hostname       string
	GRPC           *manager.Manager
}

// Interface is the main runtime for the actors subsystem.
//
//nolint:interfacebloat
type Interface interface {
	Init(InitOptions) error
	Run(context.Context) error
	Engine(context.Context) (engine.Interface, error)
	Table(context.Context) (table.Interface, error)
	State(context.Context) (actorstate.Interface, error)
	Timers(context.Context) (timers.Interface, error)
	Reminders(context.Context) (reminders.Interface, error)
	RuntimeStatus() *runtimev1pb.ActorRuntime
	RegisterHosted(hostconfig.Config) error
	UnRegisterHosted(actorTypes ...string)
	WaitForRegisteredHosts(ctx context.Context) error
}

type actors struct {
	appID              string
	namespace          string
	port               int
	placementAddresses []string
	schedulerReminders bool
	healthEndpoint     string
	resiliency         resiliency.Provider
	security           security.Handler
	schedulerClients   *clients.Clients
	healthz            healthz.Healthz
	compStore          *compstore.ComponentStore
	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool

	reminders      reminders.Interface
	table          table.Interface
	placement      placement.Interface
	engine         engine.Interface
	timerStorage   internaltimers.Storage
	timers         timers.Interface
	idlerQueue     *queue.Processor[string, targets.Idlable]
	stateReminders *statestore.Statestore
	reminderStore  storage.Interface
	state          actorstate.Interface

	disabled   *atomic.Pointer[error]
	readyCh    chan struct{}
	initDoneCh chan struct{}
	closedCh   chan struct{}

	registerDoneCh   chan struct{}
	registerDoneLock sync.RWMutex

	clock clock.Clock
}

// New create a new actors runtime with given config.
func New(opts Options) Interface {
	var disabled atomic.Pointer[error]
	if len(opts.PlacementAddresses) == 0 {
		var err error = messages.ErrActorNoPlacement
		log.Warnf("Actor runtime disabled: %s", err)
		disabled.Store(&err)
	}

	return &actors{
		appID:              opts.AppID,
		namespace:          opts.Namespace,
		port:               opts.Port,
		placementAddresses: opts.PlacementAddresses,
		schedulerReminders: opts.SchedulerReminders,
		healthEndpoint:     opts.HealthEndpoint,
		resiliency:         opts.Resiliency,
		security:           opts.Security,
		schedulerClients:   opts.SchedulerClients,
		compStore:          opts.CompStore,
		stateTTLEnabled:    opts.StateTTLEnabled,
		clock:              clock.RealClock{},
		disabled:           &disabled,
		healthz:            opts.Healthz,
		readyCh:            make(chan struct{}),
		closedCh:           make(chan struct{}),
		initDoneCh:         make(chan struct{}),
		registerDoneCh:     make(chan struct{}),
	}
}

func (a *actors) Init(opts InitOptions) error {
	defer close(a.initDoneCh)

	if a.disabled.Load() != nil {
		return nil
	}

	storeS, ok := a.compStore.GetStateStore(opts.StateStoreName)
	if !ok {
		var err error = messages.ErrActorRuntimeNotFound
		a.disabled.Store(&err)
		return nil
	}

	store, ok := storeS.(actorstate.Backend)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		var err error = messages.ErrActorRuntimeNotFound
		a.disabled.Store(&err)
		return nil
	}
	a.idlerQueue = queue.NewProcessor[string, targets.Idlable](a.handleIdleActor)
	a.table = table.New(table.Options{IdlerQueue: a.idlerQueue})

	apiLevel := apilevel.New()

	a.stateReminders = statestore.New(statestore.Options{
		Resiliency: a.resiliency,
		StateStore: store,
		Table:      a.table,
		StoreName:  opts.StateStoreName,
		APILevel:   apiLevel,
	})

	a.reminderStore = a.stateReminders
	if a.schedulerReminders {
		a.reminderStore = scheduler.New(scheduler.Options{
			Namespace:     a.namespace,
			AppID:         a.appID,
			Clients:       a.schedulerClients,
			StateReminder: a.stateReminders,
			Table:         a.table,
			Healthz:       a.healthz,
		})
	}

	var err error
	a.placement, err = placement.New(placement.Options{
		AppID:     a.appID,
		Addresses: a.placementAddresses,
		Security:  a.security,
		Table:     a.table,
		Namespace: a.namespace,
		Hostname:  opts.Hostname,
		Port:      a.port,
		Reminders: a.reminderStore,
		APILevel:  apiLevel,
		Healthz:   a.healthz,
	})
	if err != nil {
		return err
	}

	a.reminders = reminders.New(reminders.Options{
		Storage: a.reminderStore,
		Table:   a.table,
	})

	a.engine = engine.New(engine.Options{
		Namespace:          a.namespace,
		SchedulerReminders: a.schedulerReminders,
		Placement:          a.placement,
		GRPC:               opts.GRPC,
		Table:              a.table,
		Resiliency:         a.resiliency,
		IdlerQueue:         a.idlerQueue,
		Reminders:          a.reminders,
	})

	a.timerStorage = inmemory.New(inmemory.Options{
		Engine: a.engine,
	})
	a.timers = timers.New(timers.Options{
		Storage: a.timerStorage,
		Table:   a.table,
	})

	a.stateReminders.SetEngine(a.engine)

	a.state = actorstate.New(actorstate.Options{
		AppID:           a.appID,
		StoreName:       opts.StateStoreName,
		CompStore:       a.compStore,
		Resiliency:      a.resiliency,
		StateTTLEnabled: a.stateTTLEnabled,
		Table:           a.table,
		Placement:       a.placement,
	})

	return nil
}

func (a *actors) Run(ctx context.Context) error {
	defer close(a.closedCh)

	if a.disabled.Load() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.initDoneCh:
		}
	}

	close(a.readyCh)

	if err := a.disabled.Load(); err != nil {
		log.Infof("Actor runtime disabled: %s", *err)
		<-ctx.Done()
		return nil
	}

	log.Info("Actor runtime started")

	mngr := concurrency.NewRunnerCloserManager(nil,
		func(ctx context.Context) error {
			select {
			case <-a.registerDoneCh:
			case <-ctx.Done():
				return ctx.Err()
			}
			return a.placement.Run(ctx)
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			log.Info("Actor runtime shutting down")
			return nil
		},
	)

	if err := mngr.AddCloser(
		a.table,
		a.stateReminders,
		a.reminderStore,
		a.timerStorage,
		a.idlerQueue,
	); err != nil {
		return err
	}

	defer log.Info("Actor runtime stopped")
	return mngr.Run(ctx)
}

func (a *actors) Engine(ctx context.Context) (engine.Interface, error) {
	if err := a.waitForReady(ctx); err != nil {
		return nil, err
	}

	return a.engine, nil
}

func (a *actors) Table(ctx context.Context) (table.Interface, error) {
	if err := a.waitForReady(ctx); err != nil {
		return nil, err
	}

	return a.table, nil
}

func (a *actors) State(ctx context.Context) (actorstate.Interface, error) {
	if err := a.waitForReady(ctx); err != nil {
		return nil, err
	}

	return a.state, nil
}

func (a *actors) Timers(ctx context.Context) (timers.Interface, error) {
	if err := a.waitForReady(ctx); err != nil {
		return nil, err
	}

	return a.timers, nil
}

func (a *actors) Reminders(ctx context.Context) (reminders.Interface, error) {
	if err := a.waitForReady(ctx); err != nil {
		return nil, err
	}

	return a.reminders, nil
}

func (a *actors) waitForReady(ctx context.Context) error {
	if err := a.disabled.Load(); err != nil {
		return *err
	}

	select {
	case <-a.closedCh:
		return messages.ErrActorRuntimeClosed
	case <-a.readyCh:
		if err := a.disabled.Load(); err != nil {
			return *err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *actors) RegisterHosted(cfg hostconfig.Config) error {
	defer func() {
		a.registerDoneLock.Lock()
		select {
		case <-a.registerDoneCh:
		default:
			close(a.registerDoneCh)
		}
		a.registerDoneLock.Unlock()
	}()

	if err := a.disabled.Load(); err != nil {
		return nil
	}

	entityConfigs := make(map[string]api.EntityConfig)
	for _, entityConfg := range cfg.EntityConfigs {
		config := api.TranslateEntityConfig(entityConfg)
		for _, entity := range entityConfg.Entities {
			var found bool
			for _, hostedType := range cfg.HostedActorTypes {
				if hostedType == entity {
					entityConfigs[entity] = config
					found = true
					break
				}
			}

			if !found {
				log.Warnf("Configuration specified for non-hosted actor type: %s", entity)
			}
		}
	}

	drainOngoingCallTimeout := api.DefaultOngoingCallTimeout
	if len(cfg.DrainOngoingCallTimeout) > 0 {
		var err error
		drainOngoingCallTimeout, err = time.ParseDuration(cfg.DrainOngoingCallTimeout)
		if err != nil {
			return fmt.Errorf("failed to parse drain ongoing call timeout: %s", err)
		}
	}

	idleTimeout := api.DefaultIdleTimeout
	if len(cfg.DefaultIdleTimeout) > 0 {
		var err error
		idleTimeout, err = time.ParseDuration(cfg.DefaultIdleTimeout)
		if err != nil {
			return fmt.Errorf("failed to parse default actor idle timeout: %s", err)
		}
	}

	reentrancy := cfg.Reentrancy
	if reentrancy.MaxStackDepth == nil {
		reentrancy.MaxStackDepth = ptr.Of(api.DefaultReentrancyStackLimit)
	}

	factories := make([]table.ActorTypeFactory, 0, len(cfg.HostedActorTypes))
	for _, actorType := range cfg.HostedActorTypes {
		idleTimeout := idleTimeout
		reentrancy := reentrancy
		if c, ok := entityConfigs[actorType]; ok {
			idleTimeout = c.ActorIdleTimeout
			reentrancy = c.ReentrancyConfig
		}

		factories = append(factories, table.ActorTypeFactory{
			Type: actorType,
			Factory: app.Factory(app.Options{
				ActorType:   actorType,
				Reentrancy:  reentrancy,
				AppChannel:  cfg.AppChannel,
				Resiliency:  a.resiliency,
				IdleQueue:   a.idlerQueue,
				IdleTimeout: idleTimeout,
			}),
		})
	}

	a.stateReminders.SetEntityConfigsRemindersStoragePartitions(
		entityConfigs,
		cfg.RemindersStoragePartitions,
	)

	log.Infof("Registering hosted actors: %v", cfg.HostedActorTypes)
	a.table.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: factories,
		HostOptions: &table.ActorHostOptions{
			EntityConfigs:           entityConfigs,
			DrainRebalancedActors:   true,
			DrainOngoingCallTimeout: drainOngoingCallTimeout,
		},
	})

	return nil
}

func (a *actors) UnRegisterHosted(actorTypes ...string) {
	if a.disabled.Load() != nil {
		return
	}

	a.table.UnRegisterActorTypes(actorTypes...)

	a.registerDoneLock.Lock()
	a.registerDoneCh = make(chan struct{})
	a.registerDoneLock.Unlock()
}

func (a *actors) WaitForRegisteredHosts(ctx context.Context) error {
	if err := a.disabled.Load(); err != nil {
		return *err
	}

	a.registerDoneLock.RLock()
	rch := a.registerDoneCh
	a.registerDoneLock.RUnlock()

	select {
	case <-rch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *actors) handleIdleActor(target targets.Idlable) {
	if err := a.placement.Lock(context.Background()); err != nil {
		return
	}
	defer a.placement.Unlock()

	log.Debugf("Actor %s is idle, deactivating", target.Key())

	if err := a.table.Halt(context.Background(), target); err != nil {
		log.Errorf("Failed to halt actor %s: %s", target.Key(), err)
		return
	}
}

func (a *actors) RuntimeStatus() *runtimev1pb.ActorRuntime {
	const placementDisconnected = "placement: disconnected"

	select {
	case <-a.registerDoneCh:
		if a.disabled.Load() != nil {
			return &runtimev1pb.ActorRuntime{
				RuntimeStatus: runtimev1pb.ActorRuntime_DISABLED,
				Placement:     placementDisconnected,
			}
		}
	default:
		if len(a.placementAddresses) == 0 {
			return &runtimev1pb.ActorRuntime{
				Placement:     placementDisconnected,
				RuntimeStatus: runtimev1pb.ActorRuntime_DISABLED,
			}
		} else {
			return &runtimev1pb.ActorRuntime{
				RuntimeStatus: runtimev1pb.ActorRuntime_INITIALIZING,
			}
		}
	}

	hostReady := true
	statusMessage := "placement: connected"
	if !a.placement.Ready() {
		statusMessage = placementDisconnected
		hostReady = false
	}

	select {
	case <-a.readyCh:
	default:
		hostReady = false
	}

	tlen := a.table.Len()
	count := make([]*runtimev1pb.ActiveActorsCount, 0, len(tlen))
	for atype, alen := range tlen {
		count = append(count, &runtimev1pb.ActiveActorsCount{
			Type:  atype,
			Count: int32(alen), //nolint:gosec
		})
	}

	return &runtimev1pb.ActorRuntime{
		RuntimeStatus: runtimev1pb.ActorRuntime_RUNNING,
		ActiveActors:  count,
		Placement:     statusMessage,
		HostReady:     hostReady,
	}
}

// ValidateHostEnvironment validates that actors can be initialized properly given a set of parameters
// And the mode the runtime is operating in.
func ValidateHostEnvironment(mTLSEnabled bool, mode modes.DaprMode, namespace string) error {
	switch mode {
	case modes.KubernetesMode:
		if mTLSEnabled && namespace == "" {
			return messages.ErrActorNamespaceRequired
		}
	}
	return nil
}
