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
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/engine"
	"github.com/dapr/dapr/pkg/actors/internal/apilevel"
	"github.com/dapr/dapr/pkg/actors/internal/health"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage/scheduler"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage/statestore"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/internal/timers/inmemory"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app"
	"github.com/dapr/dapr/pkg/actors/timers"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
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
)

const (
	// If an idle actor is getting deactivated, but it's still busy, will be
	// re-enqueued with its idle timeout increased by this duration.
	actorBusyReEnqueueInterval = 10 * time.Second

	defaultIdleTimeout          = time.Minute * 60
	defaultOngoingCallTimeout   = time.Second * 60
	defaultReentrancyStackLimit = 32
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
	EntityConfigs              []config.EntityConfig
	DrainRebalancedActors      bool
	DrainOngoingCallTimeout    string
	RemindersStoragePartitions int

	StateStoreName     string
	HostedActorTypes   []string
	HealthHTTPClient   *http.Client
	DefaultIdleTimeout string
	Hostname           string

	GRPC       *manager.Manager
	Reentrancy config.ReentrancyConfig
	AppChannel channel.AppChannel
}

// Interface is the main runtime for the actors subsystem.
type Interface interface {
	Init(InitOptions) error
	Run(context.Context) error
	Engine(context.Context) (engine.Interface, error)
	Table(context.Context) (table.Interface, error)
	State(context.Context) (actorstate.Interface, error)
	Timers(context.Context) (timers.Interface, error)
	Reminders(context.Context) (reminders.Interface, error)
	RuntimeStatus() *runtimev1pb.ActorRuntime
}

type actors struct {
	appID              string
	namespace          string
	hostname           string
	port               int
	placementAddresses []string
	schedulerReminders bool
	healthEndpoint     string
	resiliency         resiliency.Provider
	security           security.Handler
	schedulerClients   *clients.Clients
	healthz            healthz.Healthz
	//htarget            healthz.Target
	compStore *compstore.ComponentStore
	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool

	reminders      reminders.Interface
	table          table.Interface
	placement      placement.Interface
	engine         engine.Interface
	timerStorage   internaltimers.Storage
	timers         timers.Interface
	reentrancy     config.ReentrancyConfig
	idlerQueue     *queue.Processor[string, targets.Idlable]
	appChannel     channel.AppChannel
	checker        *health.Checker
	stateReminders *statestore.Statestore
	reminderStore  storage.Interface
	state          actorstate.Interface

	hostedActorTypes   []string
	entityConfigs      map[string]requestresponse.EntityConfig
	defaultIdleTimeout time.Duration

	disabled   *atomic.Pointer[error]
	readyCh    chan struct{}
	initDoneCh chan struct{}
	closedCh   chan struct{}

	clock clock.Clock
}

// New create a new actors runtime with given config.
func New(opts Options) Interface {
	var disabled atomic.Pointer[error]
	if len(opts.PlacementAddresses) == 0 {
		var err error = messages.ErrActorNoPlacement
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
		//htarget:            opts.Healthz.AddTarget(),
		compStore:       opts.CompStore,
		stateTTLEnabled: opts.StateTTLEnabled,
		clock:           clock.RealClock{},
		disabled:        &disabled,
		healthz:         opts.Healthz,
		readyCh:         make(chan struct{}),
		closedCh:        make(chan struct{}),
		initDoneCh:      make(chan struct{}),
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

	if opts.AppChannel == nil {
		var err error = messages.ErrActorNoAppChannel
		a.disabled.Store(&err)
		return nil
	}

	checker, err := health.New(
		health.WithFailureThreshold(4),
		health.WithHealthyStateInterval(5*time.Second),
		health.WithUnHealthyStateInterval(time.Second/2),
		health.WithRequestTimeout(2*time.Second),
		health.WithHTTPClient(opts.HealthHTTPClient),
		health.WithAddress(a.healthEndpoint+"/healthz"),
		health.WithHealthz(a.healthz),
	)
	if err != nil {
		return err
	}

	entityConfigs := make(map[string]requestresponse.EntityConfig)
	for _, entityConfg := range opts.EntityConfigs {
		config := translateEntityConfig(entityConfg)
		for _, entity := range entityConfg.Entities {
			var found bool
			for _, hostedType := range opts.HostedActorTypes {
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

	drainOngoingCallTimeout := time.Duration(time.Minute)
	if len(opts.DrainOngoingCallTimeout) > 0 {
		drainOngoingCallTimeout, err = time.ParseDuration(opts.DrainOngoingCallTimeout)
		if err != nil {
			return fmt.Errorf("failed to parse drain ongoing call timeout: %s", err)
		}
	}

	idleTimeout := defaultIdleTimeout
	if len(opts.DefaultIdleTimeout) > 0 {
		idleTimeout, err = time.ParseDuration(opts.DefaultIdleTimeout)
		if err != nil {
			return fmt.Errorf("failed to parse default actor idle timeout: %s", err)
		}
	}

	a.appChannel = opts.AppChannel
	a.reentrancy = opts.Reentrancy
	a.hostedActorTypes = opts.HostedActorTypes
	a.entityConfigs = entityConfigs
	a.checker = checker
	a.defaultIdleTimeout = idleTimeout

	a.idlerQueue = queue.NewProcessor[string, targets.Idlable](a.handleIdleActor)
	a.table = table.New(table.Options{
		EntityConfigs:           entityConfigs,
		DrainRebalancedActors:   opts.DrainRebalancedActors,
		DrainOngoingCallTimeout: drainOngoingCallTimeout,
		IdlerQueue:              a.idlerQueue,
	})

	apiLevel := apilevel.New()

	a.stateReminders = statestore.New(statestore.Options{
		StateStore:                 store,
		Table:                      a.table,
		Resiliency:                 a.resiliency,
		EntityConfigs:              entityConfigs,
		StoreName:                  opts.StateStoreName,
		APILevel:                   apiLevel,
		RemindersStoragePartitions: opts.RemindersStoragePartitions,
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

	// TODO: @joshvanl: remove when placement accepts dynamic actors.
	a.registerHosted()

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

	if err := a.disabled.Load(); err != nil {
		log.Infof("Actor runtime disabled: %s", *err)
		close(a.readyCh)
		// TODO: @joshvanl
		//a.htarget.Ready()
		<-ctx.Done()
		return nil
	}

	log.Infof("Actor runtime started. Idle timeout: %v", a.defaultIdleTimeout)
	close(a.readyCh)

	mngr := concurrency.NewRunnerCloserManager(nil,
		a.checker.Run,
		a.placement.Run,
		func(ctx context.Context) error {
			ch := a.checker.HealthChannel()
			for {
				select {
				case healthy := <-ch:
					if healthy {
						// TODO: @joshvanl: enable when placement accepts dynamic actors.
						//a.registerHosted()
						//a.htarget.Ready()

					} else {

						// TODO: @joshvanl: enable when placement accepts dynamic actors.
						//a.unregisterHosted()
					}
				case <-ctx.Done():
					return nil
				}
			}
		},
	)

	if err := mngr.AddCloser(
		a.table,
		a.stateReminders,
		a.reminderStore,
		a.timerStorage,
		a.idlerQueue,
		a.checker.Close,
	); err != nil {
		return err
	}

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

func (a *actors) unregisterHosted() {
	for _, actorType := range a.hostedActorTypes {
		a.table.UnRegisterActorType(actorType)
	}
}

func (a *actors) registerHosted() {
	for _, actorType := range a.hostedActorTypes {
		idleTimeout := a.defaultIdleTimeout
		reentrancy := a.reentrancy
		if c, ok := a.entityConfigs[actorType]; ok {
			idleTimeout = c.ActorIdleTimeout
			reentrancy = c.ReentrancyConfig
		}

		a.table.RegisterActorType(actorType, app.Factory(app.Options{
			ActorType:   actorType,
			Reentrancy:  reentrancy,
			AppChannel:  a.appChannel,
			Resiliency:  a.resiliency,
			IdleQueue:   a.idlerQueue,
			IdleTimeout: idleTimeout,
		}))
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
	select {
	case <-a.initDoneCh:
		if a.disabled.Load() != nil {
			return &runtimev1pb.ActorRuntime{
				RuntimeStatus: runtimev1pb.ActorRuntime_DISABLED,
				Placement:     "placement: disconnected",
			}
		}
	default:
		if len(a.placementAddresses) == 0 {
			return &runtimev1pb.ActorRuntime{
				Placement:     "placement: disconnected",
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
		statusMessage = "placement: disconnected"
		hostReady = false
	}

	select {
	case <-a.readyCh:
	default:
		hostReady = false
	}

	var count []*runtimev1pb.ActiveActorsCount
	for atype, alen := range a.table.Len() {
		count = append(count, &runtimev1pb.ActiveActorsCount{
			Type:  atype,
			Count: int32(alen),
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
			return errors.New("actors must have a namespace configured when running in Kubernetes mode")
		}
	}
	return nil
}

func translateEntityConfig(appConfig config.EntityConfig) requestresponse.EntityConfig {
	domainConfig := requestresponse.EntityConfig{
		Entities:                   appConfig.Entities,
		ActorIdleTimeout:           defaultIdleTimeout,
		DrainOngoingCallTimeout:    defaultOngoingCallTimeout,
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		ReentrancyConfig:           appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}

	idleDuration, err := time.ParseDuration(appConfig.ActorIdleTimeout)
	if err == nil {
		domainConfig.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(appConfig.DrainOngoingCallTimeout)
	if err == nil {
		domainConfig.DrainOngoingCallTimeout = drainCallDuration
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := defaultReentrancyStackLimit
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}
