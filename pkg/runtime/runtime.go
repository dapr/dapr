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

package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/api/grpc"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/api/http"
	"github.com/dapr/dapr/pkg/api/universal"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/httpendpoint"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	middlewarehttp "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/hotreload"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/processor/workflow"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime")

// DaprRuntime holds all the core components of the runtime.
type DaprRuntime struct {
	runtimeConfig     *internalConfig
	globalConfig      *config.Configuration
	accessControlList *config.AccessControlList
	grpc              *manager.Manager
	channels          *channels.Channels
	appConfig         config.ApplicationConfig
	directMessaging   invokev1.DirectMessaging
	actor             actors.ActorRuntime

	nameResolver        nr.Resolver
	hostAddress         string
	actorStateStoreLock sync.RWMutex
	namespace           string
	podName             string
	daprUniversal       *universal.Universal
	daprHTTPAPI         http.API
	daprGRPCAPI         grpc.API
	operatorClient      operatorv1pb.OperatorClient
	isAppHealthy        chan struct{}
	appHealth           *apphealth.AppHealth
	appHealthReady      func(context.Context) error // Invoked the first time the app health becomes ready
	appHealthLock       sync.Mutex
	httpMiddleware      *middlewarehttp.HTTP
	compStore           *compstore.ComponentStore
	meta                *meta.Meta
	processor           *processor.Processor
	authz               *authorizer.Authorizer
	sec                 security.Handler
	runnerCloser        *concurrency.RunnerCloserManager
	clock               clock.Clock
	reloader            *hotreload.Reloader

	// Used for testing.
	initComplete chan struct{}

	proxy messaging.Proxy

	resiliency resiliency.Provider

	tracerProvider *sdktrace.TracerProvider

	workflowEngine *wfengine.WorkflowEngine

	wg sync.WaitGroup
}

// newDaprRuntime returns a new runtime with the given runtime config and global config.
func newDaprRuntime(ctx context.Context,
	sec security.Handler,
	runtimeConfig *internalConfig,
	globalConfig *config.Configuration,
	accessControlList *config.AccessControlList,
	resiliencyProvider resiliency.Provider,
) (*DaprRuntime, error) {
	compStore := compstore.New()

	namespace := security.CurrentNamespace()
	podName := getPodName()

	meta := meta.New(meta.Options{
		ID:            runtimeConfig.id,
		PodName:       podName,
		Namespace:     namespace,
		StrictSandbox: globalConfig.Spec.WasmSpec.GetStrictSandbox(),
		Mode:          runtimeConfig.mode,
	})

	operatorClient, err := getOperatorClient(ctx, sec, runtimeConfig)
	if err != nil {
		return nil, err
	}

	grpc := createGRPCManager(sec, runtimeConfig, globalConfig)

	authz := authorizer.New(authorizer.Options{
		ID:           runtimeConfig.id,
		Namespace:    namespace,
		GlobalConfig: globalConfig,
	})

	httpMiddleware := middlewarehttp.New()
	httpMiddlewareApp := httpMiddleware.BuildPipelineFromSpec("app", globalConfig.Spec.AppHTTPPipelineSpec)

	channels := channels.New(channels.Options{
		Registry:            runtimeConfig.registry,
		ComponentStore:      compStore,
		Meta:                meta,
		AppConnectionConfig: runtimeConfig.appConnectionConfig,
		GlobalConfig:        globalConfig,
		MaxRequestBodySize:  runtimeConfig.maxRequestBodySize,
		ReadBufferSize:      runtimeConfig.readBufferSize,
		GRPC:                grpc,
		AppMiddleware:       httpMiddlewareApp,
	})

	processor := processor.New(processor.Options{
		ID:             runtimeConfig.id,
		Namespace:      namespace,
		IsHTTP:         runtimeConfig.appConnectionConfig.Protocol.IsHTTP(),
		ActorsEnabled:  len(runtimeConfig.actorsService) > 0,
		Registry:       runtimeConfig.registry,
		ComponentStore: compStore,
		Meta:           meta,
		GlobalConfig:   globalConfig,
		Resiliency:     resiliencyProvider,
		Mode:           runtimeConfig.mode,
		PodName:        podName,
		Standalone:     runtimeConfig.standalone,
		OperatorClient: operatorClient,
		GRPC:           grpc,
		Channels:       channels,
		MiddlewareHTTP: httpMiddleware,
	})

	var reloader *hotreload.Reloader
	switch runtimeConfig.mode {
	case modes.KubernetesMode:
		reloader = hotreload.NewOperator(hotreload.OptionsReloaderOperator{
			PodName:        podName,
			Namespace:      namespace,
			Client:         operatorClient,
			Config:         globalConfig,
			ComponentStore: compStore,
			Authorizer:     authz,
			Processor:      processor,
		})
	case modes.StandaloneMode:
		reloader, err = hotreload.NewDisk(ctx, hotreload.OptionsReloaderDisk{
			Config:         globalConfig,
			Dirs:           runtimeConfig.standalone.ResourcesPath,
			ComponentStore: compStore,
			Authorizer:     authz,
			Processor:      processor,
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid mode: %s", runtimeConfig.mode)
	}

	rt := &DaprRuntime{
		runtimeConfig:     runtimeConfig,
		globalConfig:      globalConfig,
		accessControlList: accessControlList,
		grpc:              grpc,
		tracerProvider:    nil,
		resiliency:        resiliencyProvider,
		appHealthReady:    nil,
		compStore:         compStore,
		meta:              meta,
		operatorClient:    operatorClient,
		channels:          channels,
		sec:               sec,
		processor:         processor,
		authz:             authz,
		reloader:          reloader,
		namespace:         namespace,
		podName:           podName,
		initComplete:      make(chan struct{}),
		isAppHealthy:      make(chan struct{}),
		clock:             new(clock.RealClock),
		httpMiddleware:    httpMiddleware,
	}
	close(rt.isAppHealthy)

	var gracePeriod *time.Duration
	if duration := runtimeConfig.gracefulShutdownDuration; duration > 0 {
		gracePeriod = &duration
	}

	rt.runnerCloser = concurrency.NewRunnerCloserManager(gracePeriod,
		rt.runtimeConfig.metricsExporter.Run,
		rt.processor.Process,
		func(ctx context.Context) error {
			start := time.Now()
			log.Infof("%s mode configured", rt.runtimeConfig.mode)
			log.Infof("app id: %s", rt.runtimeConfig.id)

			if err := rt.initRuntime(ctx); err != nil {
				return err
			}

			d := time.Since(start).Milliseconds()
			log.Infof("dapr initialized. Status: Running. Init Elapsed %vms", d)

			if rt.daprHTTPAPI != nil {
				// Setting the status only when runtime is initialized.
				rt.daprHTTPAPI.MarkStatusAsReady()
			}

			close(rt.initComplete)
			<-ctx.Done()

			return nil
		},
	)

	if rt.reloader != nil {
		if err := rt.runnerCloser.Add(rt.reloader.Run); err != nil {
			return nil, err
		}
		if err := rt.runnerCloser.AddCloser(rt.reloader); err != nil {
			return nil, err
		}
	}

	if err := rt.runnerCloser.AddCloser(
		func() error {
			log.Info("Dapr is shutting down")
			comps := rt.compStore.ListComponents()
			errCh := make(chan error)
			for _, comp := range comps {
				go func(comp componentsV1alpha1.Component) {
					log.Infof("Shutting down component %s", comp.LogName())
					errCh <- rt.processor.Close(comp)
				}(comp)
			}

			errs := make([]error, len(comps)+1)
			for i := range comps {
				errs[i] = <-errCh
			}

			rt.wg.Wait()
			log.Info("Dapr runtime stopped")
			errs[len(comps)] = rt.cleanSockets()
			return errors.Join(errs...)
		},
		rt.stopWorkflow,
		rt.stopActor,
		rt.stopTrace,
		rt.grpc,
	); err != nil {
		return nil, err
	}

	return rt, nil
}

// Run performs initialization of the runtime with the runtime and global configurations.
func (a *DaprRuntime) Run(parentCtx context.Context) error {
	ctx := parentCtx
	if a.runtimeConfig.blockShutdownDuration != nil {
		// Override context with Background. Runner context will be cancelled when
		// blocking graceful shutdown returns.
		ctx = context.Background()
		a.runnerCloser.Add(func(ctx context.Context) error {
			select {
			case <-parentCtx.Done():
			case <-ctx.Done():
				// Return nil as another routine has returned, not due to an interrupt.
				return nil
			}

			log.Infof("Blocking graceful shutdown for %s or until app reports unhealthy...", *a.runtimeConfig.blockShutdownDuration)

			// Stop reading from subscriptions and input bindings forever while
			// blocking graceful shutdown. This will prevent incoming messages from
			// being processed, but allow outgoing APIs to be processed.
			a.processor.PubSub().StopSubscriptions(true)
			a.processor.Binding().StopReadingFromBindings(true)

			select {
			case <-a.clock.After(*a.runtimeConfig.blockShutdownDuration):
				log.Info("Block shutdown period expired, entering shutdown...")
			case <-a.isAppHealthy:
				log.Info("App reported unhealthy, entering shutdown...")
			}
			return nil
		})
	}

	return a.runnerCloser.Run(ctx)
}

func getPodName() string {
	return os.Getenv("POD_NAME")
}

func getOperatorClient(ctx context.Context, sec security.Handler, cfg *internalConfig) (operatorv1pb.OperatorClient, error) {
	// Get the operator client only if we're running in Kubernetes and if we need it
	if cfg.mode != modes.KubernetesMode {
		return nil, nil
	}

	client, _, err := client.GetOperatorClient(ctx, cfg.kubernetes.ControlPlaneAddress, sec)
	if err != nil {
		return nil, fmt.Errorf("error creating operator client: %w", err)
	}

	return client, nil
}

// setupTracing set up the trace exporters. Technically we don't need to pass `hostAddress` in,
// but we do so here to explicitly call out the dependency on having `hostAddress` computed.
func (a *DaprRuntime) setupTracing(ctx context.Context, hostAddress string, tpStore tracerProviderStore) error {
	tracingSpec := a.globalConfig.GetTracingSpec()

	// Register stdout trace exporter if user wants to debug requests or log as Info level.
	if tracingSpec.Stdout {
		tpStore.RegisterExporter(diagUtils.NewStdOutExporter())
	}

	// Register zipkin trace exporter if ZipkinSpec is specified
	if tracingSpec.Zipkin != nil && tracingSpec.Zipkin.EndpointAddress != "" {
		zipkinExporter, err := zipkin.New(tracingSpec.Zipkin.EndpointAddress)
		if err != nil {
			return err
		}
		tpStore.RegisterExporter(zipkinExporter)
	}

	// Register otel trace exporter if OtelSpec is specified
	if tracingSpec.Otel != nil && tracingSpec.Otel.EndpointAddress != "" && tracingSpec.Otel.Protocol != "" {
		endpoint := tracingSpec.Otel.EndpointAddress
		protocol := tracingSpec.Otel.Protocol
		if protocol != "http" && protocol != "grpc" {
			return fmt.Errorf("invalid protocol %v provided for Otel endpoint", protocol)
		}

		var client otlptrace.Client
		if protocol == "http" {
			clientOptions := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
			if !tracingSpec.Otel.GetIsSecure() {
				clientOptions = append(clientOptions, otlptracehttp.WithInsecure())
			}
			client = otlptracehttp.NewClient(clientOptions...)
		} else {
			clientOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
			if !tracingSpec.Otel.GetIsSecure() {
				clientOptions = append(clientOptions, otlptracegrpc.WithInsecure())
			}
			client = otlptracegrpc.NewClient(clientOptions...)
		}
		otelExporter, err := otlptrace.New(ctx, client)
		if err != nil {
			return err
		}
		tpStore.RegisterExporter(otelExporter)
	}

	if !tpStore.HasExporter() && tracingSpec.SamplingRate != "" {
		tpStore.RegisterExporter(diagUtils.NewNullExporter())
	}

	// Register a resource
	r := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(a.runtimeConfig.id),
	)

	tpStore.RegisterResource(r)

	// Register a trace sampler based on Sampling settings
	daprTraceSampler := diag.NewDaprTraceSampler(tracingSpec.SamplingRate)
	log.Infof("Dapr trace sampler initialized: %s", daprTraceSampler.Description())

	tpStore.RegisterSampler(daprTraceSampler)

	a.tracerProvider = tpStore.RegisterTracerProvider()
	return nil
}

func (a *DaprRuntime) initRuntime(ctx context.Context) error {
	var err error
	if a.hostAddress, err = utils.GetHostAddress(); err != nil {
		return fmt.Errorf("failed to determine host address: %w", err)
	}
	if err = a.setupTracing(ctx, a.hostAddress, newOpentelemetryTracerProviderStore()); err != nil {
		return fmt.Errorf("failed to setup tracing: %w", err)
	}
	// Register and initialize name resolution for service discovery.
	err = a.initNameResolution(ctx)
	if err != nil {
		log.Errorf(err.Error())
	}

	// Start proxy
	a.initProxy()

	a.initDirectMessaging(a.nameResolver)

	a.initPluggableComponents(ctx)

	a.appendBuiltinSecretStore(ctx)
	err = a.loadComponents(ctx)
	if err != nil {
		return fmt.Errorf("failed to load components: %s", err)
	}

	a.flushOutstandingComponents(ctx)

	// Creating workflow engine after components are loaded
	wfe := wfengine.NewWorkflowEngine(a.runtimeConfig.id, a.globalConfig.GetWorkflowSpec(), a.processor.WorkflowBackend())
	wfe.ConfigureGrpcExecutor()
	a.workflowEngine = wfe

	err = a.loadHTTPEndpoints(ctx)
	if err != nil {
		log.Warnf("failed to load HTTP endpoints: %s", err)
	}

	a.flushOutstandingHTTPEndpoints(ctx)

	if err = a.channels.Refresh(); err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.appConnectionConfig.Protocol), err)
	}

	// Setup allow/deny list for secrets
	a.populateSecretsConfiguration()

	// Create and start the external gRPC server
	a.daprUniversal = universal.New(universal.Options{
		AppID:                       a.runtimeConfig.id,
		Logger:                      logger.NewLogger("dapr.api"),
		CompStore:                   a.compStore,
		Resiliency:                  a.resiliency,
		Actors:                      a.actor,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		ShutdownFn:                  a.ShutdownWithWait,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
		WorkflowEngine:              wfe,
	})

	// Create and start internal and external gRPC servers
	a.daprGRPCAPI = grpc.NewAPI(grpc.APIOpts{
		Universal:             a.daprUniversal,
		Logger:                logger.NewLogger("dapr.grpc.api"),
		Channels:              a.channels,
		PubsubAdapter:         a.processor.PubSub(),
		DirectMessaging:       a.directMessaging,
		SendToOutputBindingFn: a.processor.Binding().SendToOutputBinding,
		TracingSpec:           a.globalConfig.GetTracingSpec(),
		AccessControlList:     a.accessControlList,
	})

	if err = a.runnerCloser.AddCloser(a.daprGRPCAPI); err != nil {
		return err
	}

	err = a.startGRPCAPIServer(a.daprGRPCAPI, a.runtimeConfig.apiGRPCPort)
	if err != nil {
		return fmt.Errorf("failed to start API gRPC server: %w", err)
	}
	if a.runtimeConfig.unixDomainSocket != "" {
		log.Info("API gRPC server is running on a Unix Domain Socket")
	} else {
		log.Infof("API gRPC server is running on port %v", a.runtimeConfig.apiGRPCPort)
	}

	// Start HTTP Server
	err = a.startHTTPServer(a.runtimeConfig.httpPort, a.runtimeConfig.publicPort, a.runtimeConfig.profilePort, a.runtimeConfig.allowedOrigins)
	if err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	if a.runtimeConfig.unixDomainSocket != "" {
		log.Info("HTTP server is running on a Unix Domain Socket")
	} else {
		log.Infof("HTTP server is running on port %v", a.runtimeConfig.httpPort)
	}
	log.Infof("The request body size parameter is: %v", a.runtimeConfig.maxRequestBodySize)

	// Start internal gRPC server (used for sidecar-to-sidecar communication)
	err = a.startGRPCInternalServer(a.daprGRPCAPI, a.runtimeConfig.internalGRPCPort)
	if err != nil {
		return fmt.Errorf("failed to start internal gRPC server: %w", err)
	}
	log.Infof("Internal gRPC server is running on port %v", a.runtimeConfig.internalGRPCPort)

	a.initDirectMessaging(a.nameResolver)

	if a.daprHTTPAPI != nil {
		a.daprHTTPAPI.MarkStatusAsOutboundReady()
	}
	if err := a.blockUntilAppIsReady(ctx); err != nil {
		return err
	}

	if a.runtimeConfig.appConnectionConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.appConnectionConfig.MaxConcurrency)
	}

	a.appHealthReady = a.appHealthReadyInit
	if a.runtimeConfig.appConnectionConfig.HealthCheck != nil && a.channels.AppChannel() != nil {
		// We can't just pass "a.channels.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.New(*a.runtimeConfig.appConnectionConfig.HealthCheck, func(ctx context.Context) (bool, error) {
			return a.channels.AppChannel().HealthProbe(ctx)
		})
		if err := a.runnerCloser.AddCloser(a.appHealth); err != nil {
			return err
		}
		a.appHealth.OnHealthChange(a.appHealthChanged)
		if err := a.appHealth.StartProbes(ctx); err != nil {
			return err
		}

		// Set the appHealth object in the channel so it's aware of the app's health status
		a.channels.AppChannel().SetAppHealth(a.appHealth)

		// Enqueue a probe right away
		// This will also start the input components once the app is healthy
		a.appHealth.Enqueue()
	} else {
		// If there's no health check, mark the app as healthy right away so subscriptions can start
		a.appHealthChanged(ctx, apphealth.AppStatusHealthy)
	}

	return nil
}

// appHealthReadyInit completes the initialization phase and is invoked after the app is healthy
func (a *DaprRuntime) appHealthReadyInit(ctx context.Context) (err error) {
	// Load app configuration (for actors) and init actors
	a.loadAppConfiguration(ctx)

	if a.runtimeConfig.ActorsEnabled() {
		err = a.initActors(ctx)
		if err != nil {
			log.Warn(err)
		} else {
			a.daprUniversal.SetActorRuntime(a.actor)
		}
	}

	// Initialize workflow engine
	if err = a.initWorkflowEngine(ctx); err != nil {
		return err
	}

	// We set actors as initialized whether we have an actors runtime or not
	a.daprUniversal.SetActorsInitDone()

	if cb := a.runtimeConfig.registry.ComponentsCallback(); cb != nil {
		if err = cb(registry.ComponentRegistry{
			DirectMessaging: a.directMessaging,
			CompStore:       a.compStore,
		}); err != nil {
			return fmt.Errorf("failed to register components with callback: %w", err)
		}
	}

	return nil
}

func (a *DaprRuntime) initWorkflowEngine(ctx context.Context) error {
	wfComponentFactory := wfengine.BuiltinWorkflowFactory(a.workflowEngine)

	// If actors are not enabled, still invoke SetActorRuntime on the workflow engine with `nil` to unblock startup
	if abe, ok := a.workflowEngine.Backend.(interface {
		SetActorRuntime(ctx context.Context, actorRuntime actors.ActorRuntime)
	}); ok {
		log.Info("Configuring workflow engine with actors backend")
		var actorRuntime actors.ActorRuntime
		if a.runtimeConfig.ActorsEnabled() {
			actorRuntime = a.actor
		}
		abe.SetActorRuntime(ctx, actorRuntime)
	}

	reg := a.runtimeConfig.registry.Workflows()
	if reg == nil {
		log.Info("No workflow registry available, not registering Dapr workflow component.")
		return nil
	}

	log.Info("Registering component for dapr workflow engine...")
	reg.RegisterComponent(wfComponentFactory, "dapr")
	wfe := workflow.New(workflow.Options{
		Registry:       a.runtimeConfig.registry.Workflows(),
		ComponentStore: a.compStore,
		Meta:           a.meta,
	})
	if err := wfe.Init(ctx, wfengine.ComponentDefinition()); err != nil {
		return fmt.Errorf("failed to initialize Dapr workflow component: %w", err)
	}

	log.Info("Workflow engine initialized.")

	return a.runnerCloser.AddCloser(func() error {
		return wfe.Close(wfengine.ComponentDefinition())
	})
}

// initPluggableComponents discover pluggable components and initialize with their respective registries.
func (a *DaprRuntime) initPluggableComponents(ctx context.Context) {
	if runtime.GOOS == "windows" {
		log.Debugf("the current OS does not support pluggable components feature, skipping initialization")
		return
	}
	if err := pluggable.Discover(ctx); err != nil {
		log.Errorf("could not initialize pluggable components %v", err)
	}
}

// Sets the status of the app to healthy or un-healthy
// Callback for apphealth when the detected status changed
func (a *DaprRuntime) appHealthChanged(ctx context.Context, status uint8) {
	a.appHealthLock.Lock()
	defer a.appHealthLock.Unlock()

	switch status {
	case apphealth.AppStatusHealthy:
		select {
		case <-a.isAppHealthy:
			a.isAppHealthy = make(chan struct{})
		default:
		}

		// First time the app becomes healthy, complete the init process
		if a.appHealthReady != nil {
			if err := a.appHealthReady(ctx); err != nil {
				log.Warnf("Failed to complete app init: %s ", err)
			}
			a.appHealthReady = nil
		}

		// Start subscribing to topics and reading from input bindings
		if err := a.processor.PubSub().StartSubscriptions(ctx); err != nil {
			log.Warnf("failed to subscribe to topics: %s ", err)
		}
		err := a.processor.Binding().StartReadingFromBindings(ctx)
		if err != nil {
			log.Warnf("failed to read from bindings: %s ", err)
		}

		// Start subscribing to outbox topics
		if err := a.processor.PubSub().Outbox().SubscribeToInternalTopics(ctx, a.runtimeConfig.id); err != nil {
			log.Warnf("failed to subscribe to outbox topics: %s", err)
		}
	case apphealth.AppStatusUnhealthy:
		select {
		case <-a.isAppHealthy:
		default:
			close(a.isAppHealthy)
		}

		// Stop topic subscriptions and input bindings
		a.processor.PubSub().StopSubscriptions(false)
		a.processor.Binding().StopReadingFromBindings(false)
	}
}

func (a *DaprRuntime) populateSecretsConfiguration() {
	// Populate in a map for easy lookup by store name.
	if a.globalConfig.Spec.Secrets == nil {
		return
	}

	for _, scope := range a.globalConfig.Spec.Secrets.Scopes {
		a.compStore.AddSecretsConfiguration(scope.StoreName, scope)
	}
}

func (a *DaprRuntime) initDirectMessaging(resolver nr.Resolver) {
	a.directMessaging = messaging.NewDirectMessaging(messaging.NewDirectMessagingOpts{
		AppID:              a.runtimeConfig.id,
		Namespace:          a.namespace,
		Port:               a.runtimeConfig.internalGRPCPort,
		Mode:               a.runtimeConfig.mode,
		Channels:           a.channels,
		ClientConnFn:       a.grpc.GetGRPCConnection,
		Resolver:           resolver,
		MaxRequestBodySize: a.runtimeConfig.maxRequestBodySize,
		Proxy:              a.proxy,
		ReadBufferSize:     a.runtimeConfig.readBufferSize,
		Resiliency:         a.resiliency,
		CompStore:          a.compStore,
	})
	a.runnerCloser.AddCloser(a.directMessaging)
}

func (a *DaprRuntime) initProxy() {
	a.proxy = messaging.NewProxy(messaging.ProxyOpts{
		AppClientFn:        a.grpc.GetAppClient,
		ConnectionFactory:  a.grpc.GetGRPCConnection,
		AppID:              a.runtimeConfig.id,
		ACL:                a.accessControlList,
		Resiliency:         a.resiliency,
		MaxRequestBodySize: a.runtimeConfig.maxRequestBodySize,
	})
}

func (a *DaprRuntime) startHTTPServer(port int, publicPort *int, profilePort int, allowedOrigins string) error {
	a.daprHTTPAPI = http.NewAPI(http.APIOpts{
		Universal:             a.daprUniversal,
		Channels:              a.channels,
		DirectMessaging:       a.directMessaging,
		PubsubAdapter:         a.processor.PubSub(),
		SendToOutputBindingFn: a.processor.Binding().SendToOutputBinding,
		TracingSpec:           a.globalConfig.GetTracingSpec(),
		MaxRequestBodySize:    int64(a.runtimeConfig.maxRequestBodySize) << 20, // Convert from MB to bytes
	})

	serverConf := http.ServerConfig{
		AppID:                   a.runtimeConfig.id,
		HostAddress:             a.hostAddress,
		Port:                    port,
		APIListenAddresses:      a.runtimeConfig.apiListenAddresses,
		PublicPort:              publicPort,
		ProfilePort:             profilePort,
		AllowedOrigins:          allowedOrigins,
		EnableProfiling:         a.runtimeConfig.enableProfiling,
		MaxRequestBodySizeMB:    a.runtimeConfig.maxRequestBodySize,
		UnixDomainSocket:        a.runtimeConfig.unixDomainSocket,
		ReadBufferSizeKB:        a.runtimeConfig.readBufferSize,
		EnableAPILogging:        *a.runtimeConfig.enableAPILogging,
		APILoggingObfuscateURLs: a.globalConfig.GetAPILoggingSpec().ObfuscateURLs,
		APILogHealthChecks:      !a.globalConfig.GetAPILoggingSpec().OmitHealthChecks,
	}

	server := http.NewServer(http.NewServerOpts{
		API:         a.daprHTTPAPI,
		Config:      serverConf,
		TracingSpec: a.globalConfig.GetTracingSpec(),
		MetricSpec:  a.globalConfig.GetMetricsSpec(),
		Middleware:  a.httpMiddleware.BuildPipelineFromSpec("server", a.globalConfig.Spec.HTTPPipelineSpec),
		APISpec:     a.globalConfig.GetAPISpec(),
	})
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	if err := a.runnerCloser.AddCloser(server); err != nil {
		return err
	}

	if err := a.runnerCloser.AddCloser(func() {
		a.processor.PubSub().StopSubscriptions(true)
	}); err != nil {
		return err
	}
	if err := a.runnerCloser.AddCloser(func() {
		a.processor.Binding().StopReadingFromBindings(true)
	}); err != nil {
		return err
	}

	return nil
}

func (a *DaprRuntime) startGRPCInternalServer(api grpc.API, port int) error {
	// Since GRPCInteralServer is encrypted & authenticated, it is safe to listen on *
	serverConf := a.getNewServerConfig([]string{""}, port)
	server := grpc.NewInternalServer(api, serverConf, a.globalConfig.GetTracingSpec(), a.globalConfig.GetMetricsSpec(), a.sec, a.proxy)
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	if err := a.runnerCloser.AddCloser(server); err != nil {
		return err
	}

	return nil
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := a.getNewServerConfig(a.runtimeConfig.apiListenAddresses, port)
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.GetTracingSpec(), a.globalConfig.GetMetricsSpec(), a.globalConfig.GetAPISpec(), a.proxy, a.workflowEngine)
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	if err := a.runnerCloser.AddCloser(server); err != nil {
		return err
	}

	return nil
}

func (a *DaprRuntime) getNewServerConfig(apiListenAddresses []string, port int) grpc.ServerConfig {
	// Use the trust domain value from the access control policy spec to generate the cert
	// If no access control policy has been specified, use a default value
	trustDomain := config.DefaultTrustDomain
	if a.accessControlList != nil {
		trustDomain = a.accessControlList.TrustDomain
	}
	return grpc.ServerConfig{
		AppID:                a.runtimeConfig.id,
		HostAddress:          a.hostAddress,
		Port:                 port,
		APIListenAddresses:   apiListenAddresses,
		NameSpace:            a.namespace,
		TrustDomain:          trustDomain,
		MaxRequestBodySizeMB: a.runtimeConfig.maxRequestBodySize,
		UnixDomainSocket:     a.runtimeConfig.unixDomainSocket,
		ReadBufferSizeKB:     a.runtimeConfig.readBufferSize,
		EnableAPILogging:     *a.runtimeConfig.enableAPILogging,
	}
}

func (a *DaprRuntime) initNameResolution(ctx context.Context) (err error) {
	var (
		resolver         nr.Resolver
		resolverMetadata nr.Metadata
		resolverName     string
		resolverVersion  string
	)

	if a.globalConfig.Spec.NameResolutionSpec != nil {
		resolverName = a.globalConfig.Spec.NameResolutionSpec.Component
		resolverVersion = a.globalConfig.Spec.NameResolutionSpec.Version
	}

	if resolverName == "" {
		switch a.runtimeConfig.mode {
		case modes.KubernetesMode:
			resolverName = "kubernetes"
		case modes.StandaloneMode:
			resolverName = "mdns"
		default:
			fName := utils.ComponentLogName("nr", resolverName, resolverVersion)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, fmt.Errorf("unable to determine name resolver for %s mode", string(a.runtimeConfig.mode)))
		}
	}

	if resolverVersion == "" {
		resolverVersion = components.FirstStableVersion
	}

	fName := utils.ComponentLogName("nr", resolverName, resolverVersion)
	resolver, err = a.runtimeConfig.registry.NameResolutions().Create(resolverName, resolverVersion, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "creation", resolverName)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	resolverMetadata.Name = resolverName
	if a.globalConfig.Spec.NameResolutionSpec != nil {
		resolverMetadata.Configuration = a.globalConfig.Spec.NameResolutionSpec.Configuration
	}
	resolverMetadata.Instance = nr.Instance{
		DaprHTTPPort:     a.runtimeConfig.httpPort,
		DaprInternalPort: a.runtimeConfig.internalGRPCPort,
		AppPort:          a.runtimeConfig.appConnectionConfig.Port,
		Address:          a.hostAddress,
		AppID:            a.runtimeConfig.id,
		Namespace:        a.namespace,
	}

	err = resolver.Init(ctx, resolverMetadata)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "init", resolverName)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	a.nameResolver = resolver
	if nrCloser, ok := resolver.(io.Closer); ok {
		err = a.runnerCloser.AddCloser(nrCloser)
		if err != nil {
			return err
		}
	}

	log.Infof("Initialized name resolution to %s", resolverName)
	return nil
}

func (a *DaprRuntime) initActors(ctx context.Context) error {
	err := actors.ValidateHostEnvironment(a.runtimeConfig.mTLSEnabled, a.runtimeConfig.mode, a.namespace)
	if err != nil {
		return rterrors.NewInit(rterrors.InitFailure, "actors", err)
	}
	a.actorStateStoreLock.Lock()
	defer a.actorStateStoreLock.Unlock()

	actorStateStoreName, ok := a.processor.State().ActorStateStoreName()
	if !ok {
		log.Info("actors: state store is not configured - this is okay for clients but services with hosted actors will fail to initialize!")
	}
	actorConfig := actors.NewConfig(actors.ConfigOpts{
		HostAddress:       a.hostAddress,
		AppID:             a.runtimeConfig.id,
		ActorsService:     a.runtimeConfig.actorsService,
		RemindersService:  a.runtimeConfig.remindersService,
		Port:              a.runtimeConfig.internalGRPCPort,
		Namespace:         a.namespace,
		AppConfig:         a.appConfig,
		HealthHTTPClient:  a.channels.AppHTTPClient(),
		HealthEndpoint:    a.channels.AppHTTPEndpoint(),
		AppChannelAddress: a.runtimeConfig.appConnectionConfig.ChannelAddress,
		PodName:           getPodName(),
	})

	act, err := actors.NewActors(actors.ActorsOpts{
		AppChannel:       a.channels.AppChannel(),
		GRPCConnectionFn: a.grpc.GetGRPCConnection,
		Config:           actorConfig,
		TracingSpec:      a.globalConfig.GetTracingSpec(),
		Resiliency:       a.resiliency,
		StateStoreName:   actorStateStoreName,
		CompStore:        a.compStore,
		// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
		StateTTLEnabled: a.globalConfig.IsFeatureEnabled(config.ActorStateTTL),
		Security:        a.sec,
	})
	if err != nil {
		return rterrors.NewInit(rterrors.InitFailure, "actors", err)
	}
	err = act.Init(ctx)
	if err != nil {
		return rterrors.NewInit(rterrors.InitFailure, "actors", err)
	}
	a.actor = act
	return nil
}

func (a *DaprRuntime) loadComponents(ctx context.Context) error {
	var loader components.ComponentLoader

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = components.NewKubernetesComponents(a.runtimeConfig.kubernetes, a.namespace, a.operatorClient, a.podName)
	case modes.StandaloneMode:
		loader = components.NewLocalComponents(a.runtimeConfig.standalone.ResourcesPath...)
	default:
		return nil
	}

	log.Info("Loading components…")
	comps, err := loader.LoadComponents()
	if err != nil {
		return err
	}

	authorizedComps := a.authz.GetAuthorizedObjects(comps, a.authz.IsObjectAuthorized).([]componentsV1alpha1.Component)

	// Iterate through the list twice
	// First, we look for secret stores and load those, then all other components
	// Sure, we could sort the list of authorizedComps... but this is simpler and most certainly faster
	for _, comp := range authorizedComps {
		if strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())
			if !a.processor.AddPendingComponent(ctx, comp) {
				return nil
			}
		}
	}
	for _, comp := range authorizedComps {
		if !strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())
			if !a.processor.AddPendingComponent(ctx, comp) {
				return nil
			}
		}
	}

	return nil
}

func (a *DaprRuntime) flushOutstandingHTTPEndpoints(ctx context.Context) {
	log.Info("Waiting for all outstanding http endpoints to be processed…")
	// We flush by sending a no-op http endpoint. Since the processHTTPEndpoints goroutine only reads one http endpoint at a time,
	// We know that once the no-op http endpoint is read from the channel, all previous http endpoints will have been fully processed.
	a.processor.AddPendingEndpoint(ctx, httpEndpointV1alpha1.HTTPEndpoint{})
	log.Info("All outstanding http endpoints processed")
}

func (a *DaprRuntime) flushOutstandingComponents(ctx context.Context) {
	log.Info("Waiting for all outstanding components to be processed…")
	// We flush by sending a no-op component. Since the processComponents goroutine only reads one component at a time,
	// We know that once the no-op component is read from the channel, all previous components will have been fully processed.
	a.processor.AddPendingComponent(ctx, componentsV1alpha1.Component{})
	log.Info("All outstanding components processed")
}

func (a *DaprRuntime) loadHTTPEndpoints(ctx context.Context) error {
	var loader httpendpoint.EndpointsLoader

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = httpendpoint.NewKubernetesHTTPEndpoints(a.runtimeConfig.kubernetes, a.namespace, a.operatorClient, a.podName)
	case modes.StandaloneMode:
		loader = httpendpoint.NewLocalHTTPEndpoints(a.runtimeConfig.standalone.ResourcesPath...)
	default:
		return nil
	}

	log.Info("Loading endpoints…")
	endpoints, err := loader.LoadHTTPEndpoints()
	if err != nil {
		return err
	}

	authorizedHTTPEndpoints := a.authz.GetAuthorizedObjects(endpoints, a.authz.IsObjectAuthorized).([]httpEndpointV1alpha1.HTTPEndpoint)

	for _, e := range authorizedHTTPEndpoints {
		log.Infof("Found http endpoint: %s", e.Name)
		if !a.processor.AddPendingEndpoint(ctx, e) {
			return nil
		}
	}

	return nil
}

func (a *DaprRuntime) stopActor() error {
	if a.actor != nil {
		log.Info("Shutting down actor")
		return a.actor.Close()
	}
	return nil
}

func (a *DaprRuntime) stopWorkflow(ctx context.Context) error {
	if a.workflowEngine != nil {
		log.Info("Shutting down workflow engine")
		return a.workflowEngine.Close(ctx)
	}
	return nil
}

// ShutdownWithWait will gracefully stop runtime and wait outstanding operations.
func (a *DaprRuntime) ShutdownWithWait() {
	a.runnerCloser.Close()
}

func (a *DaprRuntime) WaitUntilShutdown() {
	a.runnerCloser.WaitUntilShutdown()
}

func (a *DaprRuntime) cleanSockets() error {
	var errs []error
	if a.runtimeConfig.unixDomainSocket != "" {
		for _, s := range []string{"http", "grpc"} {
			err := os.Remove(fmt.Sprintf("%s/dapr-%s-%s.socket", a.runtimeConfig.unixDomainSocket, a.runtimeConfig.id, s))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("error removing socket file: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

func (a *DaprRuntime) blockUntilAppIsReady(ctx context.Context) error {
	if a.runtimeConfig.appConnectionConfig.Port <= 0 {
		return nil
	}

	log.Infof("application protocol: %s. waiting on port %v.  This will block until the app is listening on that port.", string(a.runtimeConfig.appConnectionConfig.Protocol), a.runtimeConfig.appConnectionConfig.Port)

	dialAddr := a.runtimeConfig.appConnectionConfig.ChannelAddress + ":" + strconv.Itoa(a.runtimeConfig.appConnectionConfig.Port)

	for {
		var (
			conn net.Conn
			err  error
		)
		dialer := &net.Dialer{
			Timeout: 500 * time.Millisecond,
		}
		if a.runtimeConfig.appConnectionConfig.Protocol.HasTLS() {
			conn, err = tls.DialWithDialer(dialer, "tcp", dialAddr, &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec
			})
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", dialAddr)
		}
		if err == nil && conn != nil {
			conn.Close()
			break
		}

		select {
		// Return
		case <-ctx.Done():
			return ctx.Err()
		// prevents overwhelming the OS with open connections
		case <-a.clock.After(time.Millisecond * 100):
		}
	}

	log.Infof("application discovered on port %v", a.runtimeConfig.appConnectionConfig.Port)

	return nil
}

func (a *DaprRuntime) loadAppConfiguration(ctx context.Context) {
	if a.channels.AppChannel() == nil {
		return
	}

	appConfig, err := a.channels.AppChannel().GetAppConfig(ctx, a.runtimeConfig.id)
	if err != nil {
		return
	}

	if appConfig != nil {
		a.appConfig = *appConfig
		log.Info("Application configuration loaded")
	}
}

func (a *DaprRuntime) appendBuiltinSecretStore(ctx context.Context) {
	if a.runtimeConfig.disableBuiltinK8sSecretStore {
		return
	}

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		// Preload Kubernetes secretstore
		a.processor.AddPendingComponent(ctx, componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: components.FirstStableVersion,
			},
		})
	}
}

func (a *DaprRuntime) getComponentsCapabilitesMap() map[string][]string {
	capabilities := make(map[string][]string)
	for key, store := range a.compStore.ListStateStores() {
		features := store.Features()
		stateStoreCapabilities := featureTypeToString(features)
		if state.FeatureETag.IsPresent(features) && state.FeatureTransactional.IsPresent(features) {
			stateStoreCapabilities = append(stateStoreCapabilities, "ACTOR")
		}
		capabilities[key] = stateStoreCapabilities
	}
	for key, pubSubItem := range a.compStore.ListPubSubs() {
		features := pubSubItem.Component.Features()
		capabilities[key] = featureTypeToString(features)
	}
	for key := range a.compStore.ListInputBindings() {
		capabilities[key] = []string{"INPUT_BINDING"}
	}
	for key := range a.compStore.ListOutputBindings() {
		if val, found := capabilities[key]; found {
			capabilities[key] = append(val, "OUTPUT_BINDING")
		} else {
			capabilities[key] = []string{"OUTPUT_BINDING"}
		}
	}
	for key, store := range a.compStore.ListSecretStores() {
		features := store.Features()
		capabilities[key] = featureTypeToString(features)
	}
	return capabilities
}

// converts components Features from FeatureType to string
func featureTypeToString(features interface{}) []string {
	featureStr := make([]string, 0)
	switch reflect.TypeOf(features).Kind() {
	case reflect.Slice:
		val := reflect.ValueOf(features)
		for i := 0; i < val.Len(); i++ {
			featureStr = append(featureStr, val.Index(i).String())
		}
	}
	return featureStr
}

func createGRPCManager(sec security.Handler, runtimeConfig *internalConfig, globalConfig *config.Configuration) *manager.Manager {
	grpcAppChannelConfig := &manager.AppChannelConfig{}
	if globalConfig != nil {
		grpcAppChannelConfig.TracingSpec = globalConfig.GetTracingSpec()
	}
	if runtimeConfig != nil {
		grpcAppChannelConfig.Port = runtimeConfig.appConnectionConfig.Port
		grpcAppChannelConfig.MaxConcurrency = runtimeConfig.appConnectionConfig.MaxConcurrency
		grpcAppChannelConfig.EnableTLS = (runtimeConfig.appConnectionConfig.Protocol == protocol.GRPCSProtocol)
		grpcAppChannelConfig.MaxRequestBodySizeMB = runtimeConfig.maxRequestBodySize
		grpcAppChannelConfig.ReadBufferSizeKB = runtimeConfig.readBufferSize
		grpcAppChannelConfig.BaseAddress = runtimeConfig.appConnectionConfig.ChannelAddress
	}

	m := manager.NewManager(sec, runtimeConfig.mode, grpcAppChannelConfig)
	m.StartCollector()
	return m
}

func (a *DaprRuntime) stopTrace(ctx context.Context) error {
	if a.tracerProvider == nil {
		return nil
	}
	// Flush and shutdown the tracing provider.
	if err := a.tracerProvider.ForceFlush(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warnf("Error flushing tracing provider: %v", err)
	}

	if err := a.tracerProvider.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("error shutting down tracing provider: %w", err)
	} else {
		a.tracerProvider = nil
	}
	return nil
}
