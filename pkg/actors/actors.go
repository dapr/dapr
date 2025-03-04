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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine"
	"github.com/dapr/dapr/pkg/actors/hostconfig"
	"github.com/dapr/dapr/pkg/actors/internal/apilevel"
	"github.com/dapr/dapr/pkg/actors/internal/locker"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
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
	Healthz            healthz.Healthz
	CompStore          *compstore.ComponentStore
	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled    bool
	MaxRequestBodySize int
}

type InitOptions struct {
	StateStoreName   string
	Hostname         string
	GRPC             *manager.Manager
	SchedulerClients clients.Clients
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
	healthz            healthz.Healthz
	compStore          *compstore.ComponentStore
	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled    bool
	maxRequestBodySize int

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
	if len(opts.PlacementAddresses) == 0 ||
		(len(opts.PlacementAddresses) == 1 && strings.TrimSpace(strings.Trim(opts.PlacementAddresses[0], `"'`)) == "") {
		var err error = messages.ErrActorNoPlacement
		log.Warnf("Actor runtime disabled: %s. Actors and Workflow APIs will be unavailable", err)
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
		compStore:          opts.CompStore,
		stateTTLEnabled:    opts.StateTTLEnabled,
		clock:              clock.RealClock{},
		disabled:           &disabled,
		healthz:            opts.Healthz,
		readyCh:            make(chan struct{}),
		closedCh:           make(chan struct{}),
		initDoneCh:         make(chan struct{}),
		registerDoneCh:     make(chan struct{}),
		maxRequestBodySize: opts.MaxRequestBodySize,
	}
}

func (a *actors) Init(opts InitOptions) error {
	defer close(a.initDoneCh)

	if a.disabled.Load() != nil {
		return nil
	}

	a.idlerQueue = queue.NewProcessor[string, targets.Idlable](a.handleIdleActor)

	rStore := reentrancystore.New()

	locker := locker.New(locker.Options{
		ConfigStore: rStore,
	})

	a.table = table.New(table.Options{
		IdlerQueue:      a.idlerQueue,
		Locker:          locker,
		ReentrancyStore: rStore,
	})

	apiLevel := apilevel.New()

	storeEnabled := a.buildStateStore(opts, apiLevel)

	if a.reminderStore != nil {
		a.reminders = reminders.New(reminders.Options{
			Storage: a.reminderStore,
			Table:   a.table,
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

	if storeEnabled {
		a.state = actorstate.New(actorstate.Options{
			AppID:           a.appID,
			StoreName:       opts.StateStoreName,
			CompStore:       a.compStore,
			Resiliency:      a.resiliency,
			StateTTLEnabled: a.stateTTLEnabled,
			Table:           a.table,
			Placement:       a.placement,
		})
	}

	a.engine = engine.New(engine.Options{
		Namespace:          a.namespace,
		SchedulerReminders: a.schedulerReminders,
		Placement:          a.placement,
		GRPC:               opts.GRPC,
		Table:              a.table,
		Resiliency:         a.resiliency,
		IdlerQueue:         a.idlerQueue,
		Reminders:          a.reminders,
		Locker:             locker,
		MaxRequestBodySize: a.maxRequestBodySize,
	})

	a.timerStorage = inmemory.New(inmemory.Options{
		Engine: a.engine,
	})
	a.timers = timers.New(timers.Options{
		Storage: a.timerStorage,
		Table:   a.table,
	})

	if a.stateReminders != nil {
		a.stateReminders.SetEngine(a.engine)
	}

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
			// Only wait for host registration before starting the placement client,
			// since registering Actor host types is dependent on the Actor state
			// store being configured.
			if a.state != nil {
				select {
				case <-a.registerDoneCh:
				case <-ctx.Done():
					return ctx.Err()
				}
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
		a.timerStorage,
		a.idlerQueue,
	); err != nil {
		return err
	}

	if a.stateReminders != nil {
		if err := mngr.AddCloser(
			a.stateReminders,
			a.reminderStore,
		); err != nil {
			return err
		}
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
			Type:       actorType,
			Reentrancy: reentrancy,
			Factory: app.Factory(app.Options{
				ActorType:   actorType,
				AppChannel:  cfg.AppChannel,
				Resiliency:  a.resiliency,
				IdleQueue:   a.idlerQueue,
				IdleTimeout: idleTimeout,
			}),
		})
	}

	if a.stateReminders != nil {
		a.stateReminders.SetEntityConfigsRemindersStoragePartitions(
			entityConfigs,
			cfg.RemindersStoragePartitions,
		)
	}

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
	// We don't use the placement context here as we are already deactivating the
	// actor.
	_, cancel, err := a.placement.Lock(context.Background())
	if err != nil {
		log.Errorf("Failed to lock placement for idle actor deactivation: %s", err)
		return
	}
	defer cancel()

	log.Debugf("Actor %s is idle, deactivating", target.Key())

	if err := a.table.HaltIdlable(context.Background(), target); err != nil {
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

func (a *actors) buildStateStore(opts InitOptions, apiLevel *apilevel.APILevel) bool {
	storeS, ok := a.compStore.GetStateStore(opts.StateStoreName)
	if !ok {
		log.Info("Actor state store not configured - actor hosting disabled, but invocation enabled")
		return false
	}

	store, ok := storeS.(actorstate.Backend)
	if !ok {
		log.Warn("Actor state management disabled")
		return false
	}

	if !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		log.Warnf("Actor state store %s does not support required features: %s, %s", opts.StateStoreName, state.FeatureETag, state.FeatureTransactional)
		return false
	}

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
			Clients:       opts.SchedulerClients,
			StateReminder: a.stateReminders,
			Table:         a.table,
			Healthz:       a.healthz,
		})
	}

	return true
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
