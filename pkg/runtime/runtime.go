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
	"net"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/hostconfig"
	"github.com/dapr/dapr/pkg/api/grpc"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/api/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/api/http"
	"github.com/dapr/dapr/pkg/api/universal"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	endpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	middlewarehttp "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/outbox"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/hotreload"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/pubsub/publisher"
	"github.com/dapr/dapr/pkg/runtime/pubsub/streamer"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/scheduler"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
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
	actors            actors.Interface
	wfengine          wfengine.Interface

	nameResolver          nr.Resolver
	hostAddress           string
	namespace             string
	podName               string
	daprUniversal         *universal.Universal
	daprHTTPAPI           http.API
	daprGRPCAPI           grpc.API
	operatorClient        operatorv1pb.OperatorClient
	jobsManager           *scheduler.Scheduler
	isAppHealthy          chan struct{}
	appHealth             *apphealth.AppHealth
	appHealthReady        func(context.Context) error // Invoked the first time the app health becomes ready
	appHealthLock         sync.Mutex
	httpMiddleware        *middlewarehttp.HTTP
	compStore             *compstore.ComponentStore
	pubsubAdapter         pubsub.Adapter
	pubsubAdapterStreamer pubsub.AdapterStreamer
	outbox                outbox.Outbox
	meta                  *meta.Meta
	processor             *processor.Processor
	authz                 *authorizer.Authorizer
	sec                   security.Handler
	runnerCloser          *concurrency.RunnerCloserManager
	clock                 clock.Clock
	reloader              *hotreload.Reloader

	grpcAPIServer      grpc.Server
	grpcInternalServer grpc.Server

	// Used for testing.
	initComplete chan struct{}

	proxy messaging.Proxy

	resiliency resiliency.Provider

	tracerProvider *sdktrace.TracerProvider

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
	// TODO: @joshvanl: find a solution for this:
	// We need to register our custom proxy codec in the global registrar, but
	// only after all gRPC internal codecs have been registered. This is because
	// we are squatting the conflicted base name "proto" which we use to inject
	// our custom marshal code. We do this to optionally passthrough proxy data
	// if the gRPC frame encoding is a message we don't recognise- i.e. a user is
	// doing a direct message using their own message type.
	// Since 'd' comes before 'g' in the alphabet and go `init` func execution
	// order is now sane, we have to register our conflicting base codec as
	// runtime.
	// It is also the case that gRPC uses a global variable codec registrar so we
	// can't build a custom one that we propagate.
	// The solution is to keep this as is, or find a way to bypass the codec
	// stack further down the transport layer in a wrapper.
	// I assume we can use a custom message type to wrap user messages which does
	// not have a conflicting name with the base codec.
	codec.Register()

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

	pubsubAdapter := publisher.New(publisher.Options{
		AppID:       runtimeConfig.id,
		Namespace:   namespace,
		Resiliency:  resiliencyProvider,
		GetPubSubFn: compStore.GetPubSub,
	})
	pubsubAdapterStreamer := streamer.New(ctx, streamer.Options{
		TracingSpec: globalConfig.Spec.TracingSpec,
	})
	outbox := pubsub.NewOutbox(pubsub.OptionsOutbox{
		Publisher:             pubsubAdapter,
		GetPubsubFn:           compStore.GetPubSubComponent,
		GetStateFn:            compStore.GetStateStore,
		CloudEventExtractorFn: pubsub.ExtractCloudEventProperty,
		Namespace:             namespace,
	})

	actors := actors.New(actors.Options{
		AppID:     runtimeConfig.id,
		Namespace: namespace,
		Port:      runtimeConfig.internalGRPCPort,
		// TODO: @joshvanl
		PlacementAddresses: strings.Split(strings.TrimPrefix(runtimeConfig.actorsService, "placement:"), ","),
		SchedulerReminders: globalConfig.IsFeatureEnabled(config.SchedulerReminders),
		HealthEndpoint:     channels.AppHTTPEndpoint(),
		Resiliency:         resiliencyProvider,
		Security:           sec,
		Healthz:            runtimeConfig.healthz,
		CompStore:          compStore,
		StateTTLEnabled:    globalConfig.IsFeatureEnabled(config.ActorStateTTL),
		MaxRequestBodySize: runtimeConfig.maxRequestBodySize,
		Mode:               runtimeConfig.mode,
	})

	processor := processor.New(processor.Options{
		ID:              runtimeConfig.id,
		Namespace:       namespace,
		IsHTTP:          runtimeConfig.appConnectionConfig.Protocol.IsHTTP(),
		ActorsEnabled:   len(runtimeConfig.actorsService) > 0,
		Actors:          actors,
		Registry:        runtimeConfig.registry,
		ComponentStore:  compStore,
		Meta:            meta,
		GlobalConfig:    globalConfig,
		Resiliency:      resiliencyProvider,
		Mode:            runtimeConfig.mode,
		PodName:         podName,
		OperatorClient:  operatorClient,
		GRPC:            grpc,
		Channels:        channels,
		MiddlewareHTTP:  httpMiddleware,
		Security:        sec,
		Outbox:          outbox,
		Adapter:         pubsubAdapter,
		AdapterStreamer: pubsubAdapterStreamer,
		Reporter:        runtimeConfig.registry.Reporter(),
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
			Healthz:        runtimeConfig.healthz,
		})
	case modes.StandaloneMode:
		reloader, err = hotreload.NewDisk(hotreload.OptionsReloaderDisk{
			Config:         globalConfig,
			Dirs:           runtimeConfig.standalone.ResourcesPath,
			ComponentStore: compStore,
			Authorizer:     authz,
			Processor:      processor,
			AppID:          runtimeConfig.id,
			Healthz:        runtimeConfig.healthz,
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid mode: %s", runtimeConfig.mode)
	}

	wfe := wfengine.New(wfengine.Options{
		AppID:                     runtimeConfig.id,
		Namespace:                 namespace,
		Actors:                    actors,
		Spec:                      globalConfig.Spec.WorkflowSpec,
		BackendManager:            processor.WorkflowBackend(),
		Resiliency:                resiliencyProvider,
		SchedulerReminders:        globalConfig.IsFeatureEnabled(config.SchedulerReminders),
		EventSink:                 runtimeConfig.workflowEventSink,
		EnableClusteredDeployment: globalConfig.IsFeatureEnabled(config.WorkflowsClusteredDeployment),
	})

	jobsManager, err := scheduler.New(scheduler.Options{
		Namespace:          namespace,
		AppID:              runtimeConfig.id,
		Channels:           channels,
		Actors:             actors,
		Addresses:          runtimeConfig.schedulerAddress,
		Security:           sec,
		WFEngine:           wfe,
		Healthz:            runtimeConfig.healthz,
		SchedulerReminders: globalConfig.IsFeatureEnabled(config.SchedulerReminders),
		SchedulerStreams:   runtimeConfig.schedulerStreams,
	})
	if err != nil {
		return nil, err
	}

	rt := &DaprRuntime{
		runtimeConfig:         runtimeConfig,
		globalConfig:          globalConfig,
		accessControlList:     accessControlList,
		grpc:                  grpc,
		tracerProvider:        nil,
		resiliency:            resiliencyProvider,
		appHealthReady:        nil,
		compStore:             compStore,
		pubsubAdapter:         pubsubAdapter,
		pubsubAdapterStreamer: pubsubAdapterStreamer,
		outbox:                outbox,
		meta:                  meta,
		operatorClient:        operatorClient,
		channels:              channels,
		sec:                   sec,
		processor:             processor,
		jobsManager:           jobsManager,
		authz:                 authz,
		reloader:              reloader,
		namespace:             namespace,
		podName:               podName,
		initComplete:          make(chan struct{}),
		isAppHealthy:          make(chan struct{}),
		clock:                 new(clock.RealClock),
		httpMiddleware:        httpMiddleware,
		actors:                actors,
		wfengine:              wfe,
	}
	close(rt.isAppHealthy)

	var gracePeriod *time.Duration
	if duration := runtimeConfig.gracefulShutdownDuration; duration > 0 {
		gracePeriod = &duration
	}

	rtHealthz := rt.runtimeConfig.healthz.AddTarget("runtime")
	rt.runnerCloser = concurrency.NewRunnerCloserManager(log, gracePeriod,
		rt.runtimeConfig.metricsExporter.Start,
		rt.processor.Process,
		rt.reloader.Run,
		rt.actors.Run,
		rt.wfengine.Run,
		rt.jobsManager.Run,
		func(ctx context.Context) error {
			start := time.Now()
			log.Infof("%s mode configured", rt.runtimeConfig.mode)
			log.Infof("app id: %s", rt.runtimeConfig.id)

			if rerr := rt.initRuntime(ctx); rerr != nil {
				return rerr
			}

			d := time.Since(start).Milliseconds()
			log.Infof("dapr initialized. Status: Running. Init Elapsed %vms", d)

			rtHealthz.Ready()

			close(rt.initComplete)
			<-ctx.Done()

			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			if server := rt.grpcInternalServer; server != nil {
				return server.Close()
			}
			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			if server := rt.grpcAPIServer; server != nil {
				return server.Close()
			}
			return nil
		},
	)

	if err := rt.runnerCloser.AddCloser(
		func() error {
			log.Info("Dapr is shutting down")
			comps := rt.compStore.ListComponents()
			errCh := make(chan error)
			for _, comp := range comps {
				go func(comp compapi.Component) {
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

			a.processor.Subscriber().StopAllSubscriptionsForever()
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
			if tracingSpec.Otel.Headers != "" {
				headers, err := config.StringToHeader(tracingSpec.Otel.Headers)
				if err != nil {
					return fmt.Errorf("invalid headers provided for Otel endpoint: %w", err)
				}
				clientOptions = append(clientOptions, otlptracehttp.WithHeaders(headers))
			}
			if tracingSpec.Otel.Timeout > 0 {
				clientOptions = append(clientOptions, otlptracehttp.WithTimeout(time.Duration(tracingSpec.Otel.Timeout)*time.Millisecond))
			}
			client = otlptracehttp.NewClient(clientOptions...)
		} else {
			clientOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
			if !tracingSpec.Otel.GetIsSecure() {
				clientOptions = append(clientOptions, otlptracegrpc.WithInsecure())
			}
			if tracingSpec.Otel.Headers != "" {
				headers, err := config.StringToHeader(tracingSpec.Otel.Headers)
				if err != nil {
					return fmt.Errorf("invalid headers provided for Otel endpoint: %w", err)
				}
				clientOptions = append(clientOptions, otlptracegrpc.WithHeaders(headers))
			}
			if tracingSpec.Otel.Timeout > 0 {
				clientOptions = append(clientOptions, otlptracegrpc.WithTimeout(time.Duration(tracingSpec.Otel.Timeout)*time.Millisecond))
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
		semconv.ServiceNameKey.String(getOtelServiceName(a.runtimeConfig.id)),
	)

	tpStore.RegisterResource(r)

	// Register a trace sampler based on Sampling settings
	daprTraceSampler := diag.NewDaprTraceSampler(tracingSpec.SamplingRate)
	log.Infof("Dapr trace sampler initialized: %s", daprTraceSampler.Description())

	tpStore.RegisterSampler(daprTraceSampler)

	a.tracerProvider = tpStore.RegisterTracerProvider()
	return nil
}

func getOtelServiceName(fallback string) string {
	if value := os.Getenv("OTEL_SERVICE_NAME"); value != "" {
		return value
	}
	return fallback
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

	err = a.loadHTTPEndpoints(ctx)
	if err != nil {
		log.Warnf("failed to load HTTP endpoints: %s", err)
	}
	a.flushOutstandingHTTPEndpoints(ctx)

	err = a.loadDeclarativeSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("failed to load declarative subscriptions: %s", err)
	}

	if err = a.channels.Refresh(); err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.appConnectionConfig.Protocol), err)
	}

	// Setup allow/deny list for secrets
	a.populateSecretsConfiguration()

	a.namespace = security.CurrentNamespace()

	// Create and start the external gRPC server
	a.daprUniversal = universal.New(universal.Options{
		AppID:                       a.runtimeConfig.id,
		Namespace:                   a.namespace,
		Logger:                      logger.NewLogger("dapr.api"),
		CompStore:                   a.compStore,
		Resiliency:                  a.resiliency,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		ShutdownFn:                  a.ShutdownWithWait,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
		Scheduler:                   a.jobsManager.Client(),
		Actors:                      a.actors,
		WorkflowEngine:              a.wfengine,
	})

	// Create and start internal and external gRPC servers
	a.daprGRPCAPI = grpc.NewAPI(grpc.APIOpts{
		Universal:             a.daprUniversal,
		Logger:                logger.NewLogger("dapr.grpc.api"),
		Channels:              a.channels,
		PubSubAdapter:         a.pubsubAdapter,
		PubSubAdapterStreamer: a.pubsubAdapterStreamer,
		Outbox:                a.outbox,
		DirectMessaging:       a.directMessaging,
		SendToOutputBindingFn: a.processor.Binding().SendToOutputBinding,
		TracingSpec:           a.globalConfig.GetTracingSpec(),
		AccessControlList:     a.accessControlList,
		Processor:             a.processor,
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
	err = a.startHTTPServer()
	if err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	if a.runtimeConfig.unixDomainSocket != "" {
		log.Info("HTTP server is running on a Unix Domain Socket")
	} else {
		log.Infof("HTTP server is running on port %v", a.runtimeConfig.httpPort)
	}
	log.Infof("The request body size parameter is: %v bytes", a.runtimeConfig.maxRequestBodySize)

	// Start internal gRPC server (used for sidecar-to-sidecar communication)
	err = a.startGRPCInternalServer(a.daprGRPCAPI)
	if err != nil {
		return fmt.Errorf("failed to start internal gRPC server: %w", err)
	}
	log.Infof("Internal gRPC server is running on %s:%d", a.runtimeConfig.internalGRPCListenAddress, a.runtimeConfig.internalGRPCPort)

	a.initDirectMessaging(a.nameResolver)

	if err := a.initActors(ctx); err != nil {
		return fmt.Errorf("failed to initialize actors: %w", err)
	}

	a.runtimeConfig.outboundHealthz.AddTarget("app").Ready()
	if err := a.blockUntilAppIsReady(ctx); err != nil {
		return err
	}

	if a.runtimeConfig.appConnectionConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.appConnectionConfig.MaxConcurrency)
	}

	a.appHealthReady = a.appHealthReadyInit
	if a.runtimeConfig.appConnectionConfig.HealthCheck != nil && a.channels.AppChannel() != nil {
		// We can't just pass "a.channels.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.New(*a.runtimeConfig.appConnectionConfig.HealthCheck, func(ctx context.Context) (*apphealth.Status, error) {
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
		a.appHealthChanged(ctx, apphealth.NewStatus(true, nil))
	}

	return nil
}

// appHealthReadyInit completes the initialization phase and is invoked after the app is healthy
func (a *DaprRuntime) appHealthReadyInit(ctx context.Context) error {
	// Load app configuration (for actors) and init actors
	a.loadAppConfiguration(ctx)

	if cb := a.runtimeConfig.registry.ComponentsCallback(); cb != nil {
		if err := cb(registry.ComponentRegistry{
			DirectMessaging: a.directMessaging,
			CompStore:       a.compStore,
		}); err != nil {
			return fmt.Errorf("failed to register components with callback: %w", err)
		}
	}

	return nil
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
func (a *DaprRuntime) appHealthChanged(ctx context.Context, status *apphealth.Status) {
	a.appHealthLock.Lock()
	defer a.appHealthLock.Unlock()

	if status.IsHealthy {
		select {
		case <-a.isAppHealthy:
			a.isAppHealthy = make(chan struct{})
		default:
		}

		// First time the app becomes healthy, complete the init process
		if a.appHealthReady != nil {
			if err := a.appHealthReady(ctx); err != nil {
				// TODO: @joshvanl: this must return error to error exit
				log.Warnf("Failed to complete app init: %s ", err)
			}
			a.appHealthReady = nil
		}

		if a.channels.AppChannel() != nil {
			// Start subscribing to topics and reading from input bindings
			if err := a.processor.Subscriber().StartAppSubscriptions(); err != nil {
				log.Warnf("failed to subscribe to topics: %s ", err)
			}
			err := a.processor.Binding().StartReadingFromBindings(ctx)
			if err != nil {
				log.Warnf("failed to read from bindings: %s ", err)
			}
		}

		// Start subscribing to outbox topics
		if err := a.outbox.SubscribeToInternalTopics(ctx, a.runtimeConfig.id); err != nil {
			log.Warnf("failed to subscribe to outbox topics: %s", err)
		}

		if err := a.actors.RegisterHosted(hostconfig.Config{
			EntityConfigs:              a.appConfig.EntityConfigs,
			DrainRebalancedActors:      a.appConfig.DrainRebalancedActors,
			DrainOngoingCallTimeout:    a.appConfig.DrainOngoingCallTimeout,
			RemindersStoragePartitions: a.appConfig.RemindersStoragePartitions,
			HostedActorTypes:           a.appConfig.Entities,
			DefaultIdleTimeout:         a.appConfig.ActorIdleTimeout,
			Reentrancy:                 a.appConfig.Reentrancy,
			AppChannel:                 a.channels.AppChannel(),
		}); err != nil {
			log.Warnf("Failed to register hosted actors: %s", err)
		}

		a.jobsManager.StartApp()
	} else {
		select {
		case <-a.isAppHealthy:
		default:
			close(a.isAppHealthy)
		}

		a.jobsManager.StopApp()

		// Stop topic subscriptions and input bindings
		a.processor.Subscriber().StopAppSubscriptions()
		a.processor.Binding().StopReadingFromBindings(false)

		a.actors.UnRegisterHosted(a.appConfig.Entities...)
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

func (a *DaprRuntime) startHTTPServer() error {
	getMetricSpec := a.globalConfig.GetMetricsSpec()
	a.daprHTTPAPI = http.NewAPI(http.APIOpts{
		Universal:             a.daprUniversal,
		Channels:              a.channels,
		DirectMessaging:       a.directMessaging,
		PubSubAdapter:         a.pubsubAdapter,
		Outbox:                a.outbox,
		SendToOutputBindingFn: a.processor.Binding().SendToOutputBinding,
		TracingSpec:           a.globalConfig.GetTracingSpec(),
		MetricSpec:            &getMetricSpec,
		MaxRequestBodySize:    int64(a.runtimeConfig.maxRequestBodySize),
		Healthz:               a.runtimeConfig.healthz,
		OutboundHealthz:       a.runtimeConfig.outboundHealthz,
	})

	serverConf := http.ServerConfig{
		AppID:                   a.runtimeConfig.id,
		HostAddress:             a.hostAddress,
		Port:                    a.runtimeConfig.httpPort,
		APIListenAddresses:      a.runtimeConfig.apiListenAddresses,
		PublicPort:              a.runtimeConfig.publicPort,
		PublicListenAddress:     a.runtimeConfig.publicListenAddress,
		ProfilePort:             a.runtimeConfig.profilePort,
		AllowedOrigins:          a.runtimeConfig.allowedOrigins,
		EnableProfiling:         a.runtimeConfig.enableProfiling,
		MaxRequestBodySize:      a.runtimeConfig.maxRequestBodySize,
		UnixDomainSocket:        a.runtimeConfig.unixDomainSocket,
		ReadBufferSize:          a.runtimeConfig.readBufferSize,
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

	return nil
}

func (a *DaprRuntime) startGRPCInternalServer(api grpc.API) error {
	// Since GRPCInteralServer is encrypted & authenticated, it is safe to listen on *
	serverConf := a.getNewServerConfig([]string{a.runtimeConfig.internalGRPCListenAddress}, a.runtimeConfig.internalGRPCPort)
	a.grpcInternalServer = grpc.NewInternalServer(grpc.OptionsInternal{
		API:         api,
		Config:      serverConf,
		TracingSpec: a.globalConfig.GetTracingSpec(),
		MetricSpec:  a.globalConfig.GetMetricsSpec(),
		Security:    a.sec,
		Proxy:       a.proxy,
		Healthz:     a.runtimeConfig.healthz,
	})

	if err := a.grpcInternalServer.StartNonBlocking(); err != nil {
		return err
	}

	return nil
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := a.getNewServerConfig(a.runtimeConfig.apiListenAddresses, port)
	a.grpcAPIServer = grpc.NewAPIServer(grpc.Options{
		API:            api,
		Config:         serverConf,
		TracingSpec:    a.globalConfig.GetTracingSpec(),
		MetricSpec:     a.globalConfig.GetMetricsSpec(),
		APISpec:        a.globalConfig.GetAPISpec(),
		Proxy:          a.proxy,
		WorkflowEngine: a.wfengine,
		Healthz:        a.runtimeConfig.healthz,
	})

	if err := a.grpcAPIServer.StartNonBlocking(); err != nil {
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
		AppID:              a.runtimeConfig.id,
		HostAddress:        a.hostAddress,
		Port:               port,
		APIListenAddresses: apiListenAddresses,
		NameSpace:          a.namespace,
		TrustDomain:        trustDomain,
		MaxRequestBodySize: a.runtimeConfig.maxRequestBodySize,
		UnixDomainSocket:   a.runtimeConfig.unixDomainSocket,
		ReadBufferSize:     a.runtimeConfig.readBufferSize,
		EnableAPILogging:   *a.runtimeConfig.enableAPILogging,
	}
}

func (a *DaprRuntime) initNameResolution(ctx context.Context) (err error) {
	var (
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
	a.nameResolver, err = a.runtimeConfig.registry.NameResolutions().Create(resolverName, resolverVersion, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "creation", resolverName)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	resolverMetadata.Name = resolverName
	if a.globalConfig.Spec.NameResolutionSpec != nil {
		resolverMetadata.Configuration = a.globalConfig.Spec.NameResolutionSpec.Configuration
	}
	// Override host address if the internal gRPC listen address is localhost.
	hostAddress := a.hostAddress
	if utils.Contains(
		[]string{"127.0.0.1", "localhost", "[::1]"},
		a.runtimeConfig.internalGRPCListenAddress,
	) {
		hostAddress = a.runtimeConfig.internalGRPCListenAddress
	}
	resolverMetadata.Instance = nr.Instance{
		DaprHTTPPort:     a.runtimeConfig.httpPort,
		DaprInternalPort: a.runtimeConfig.internalGRPCPort,
		AppPort:          a.runtimeConfig.appConnectionConfig.Port,
		Address:          hostAddress,
		AppID:            a.runtimeConfig.id,
		Namespace:        a.namespace,
	}

	err = a.nameResolver.Init(ctx, resolverMetadata)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "init", resolverName)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	if err = a.runnerCloser.AddCloser(a.nameResolver); err != nil {
		return err
	}

	log.Infof("Initialized name resolution to %s", resolverName)
	return nil
}

func (a *DaprRuntime) initActors(ctx context.Context) error {
	err := actors.ValidateHostEnvironment(a.runtimeConfig.mTLSEnabled, a.runtimeConfig.mode, a.namespace)
	if err != nil {
		return rterrors.NewInit(rterrors.InitFailure, "actors", err)
	}

	actorStateStoreName, ok := a.processor.State().ActorStateStoreName()
	if !ok {
		log.Info("actors: state store is not configured - this is okay for clients but services with hosted actors will fail to initialize!")
	}

	// Override host address if the internal gRPC listen address is localhost.
	hostAddress := a.hostAddress
	if utils.Contains(
		[]string{"127.0.0.1", "localhost", "[::1]"},
		a.runtimeConfig.internalGRPCListenAddress,
	) {
		hostAddress = a.runtimeConfig.internalGRPCListenAddress
	}

	if err := a.actors.Init(actors.InitOptions{
		Hostname:          hostAddress,
		StateStoreName:    actorStateStoreName,
		GRPC:              a.grpc,
		SchedulerClient:   a.jobsManager.Client(),
		SchedulerReloader: a.jobsManager,
	}); err != nil {
		return err
	}

	return nil
}

func (a *DaprRuntime) loadComponents(ctx context.Context) error {
	var loader loader.Loader[compapi.Component]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = kubernetes.NewComponents(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
			PodName:   a.podName,
		})
	case modes.StandaloneMode:
		loader = disk.NewComponents(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading components…")
	comps, err := loader.Load(ctx)
	if err != nil {
		return err
	}

	authorizedComps := a.authz.GetAuthorizedObjects(comps, a.authz.IsObjectAuthorized).([]compapi.Component)

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

func (a *DaprRuntime) loadDeclarativeSubscriptions(ctx context.Context) error {
	var loader loader.Loader[subapi.Subscription]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = kubernetes.NewSubscriptions(kubernetes.Options{
			Client:    a.operatorClient,
			Namespace: a.namespace,
			PodName:   a.podName,
		})
	case modes.StandaloneMode:
		loader = disk.NewSubscriptions(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading Declarative Subscriptions…")
	subs, err := loader.Load(ctx)
	if err != nil {
		return err
	}

	for _, s := range subs {
		log.Infof("Found Subscription: %s", s.Name)
	}

	a.processor.AddPendingSubscription(ctx, subs...)

	return nil
}

func (a *DaprRuntime) flushOutstandingHTTPEndpoints(ctx context.Context) {
	log.Info("Waiting for all outstanding http endpoints to be processed…")
	// We flush by sending a no-op http endpoint. Since the processHTTPEndpoints goroutine only reads one http endpoint at a time,
	// We know that once the no-op http endpoint is read from the channel, all previous http endpoints will have been fully processed.
	a.processor.AddPendingEndpoint(ctx, endpointapi.HTTPEndpoint{})
	log.Info("All outstanding http endpoints processed")
}

func (a *DaprRuntime) flushOutstandingComponents(ctx context.Context) {
	log.Info("Waiting for all outstanding components to be processed…")
	// We flush by sending a no-op component. Since the processComponents goroutine only reads one component at a time,
	// We know that once the no-op component is read from the channel, all previous components will have been fully processed.
	a.processor.AddPendingComponent(ctx, compapi.Component{})
	log.Info("All outstanding components processed")
}

func (a *DaprRuntime) loadHTTPEndpoints(ctx context.Context) error {
	var loader loader.Loader[endpointapi.HTTPEndpoint]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = kubernetes.NewHTTPEndpoints(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
			PodName:   a.podName,
		})
	case modes.StandaloneMode:
		loader = disk.NewHTTPEndpoints(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading endpoints…")
	endpoints, err := loader.Load(ctx)
	if err != nil {
		return err
	}

	authorizedHTTPEndpoints := a.authz.GetAuthorizedObjects(endpoints, a.authz.IsObjectAuthorized).([]endpointapi.HTTPEndpoint)

	for _, e := range authorizedHTTPEndpoints {
		log.Infof("Found http endpoint: %s", e.Name)
		if !a.processor.AddPendingEndpoint(ctx, e) {
			return nil
		}
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
		a.processor.AddPendingComponent(ctx, compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: compapi.ComponentSpec{
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
		for i := range val.Len() {
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
		grpcAppChannelConfig.MaxRequestBodySize = runtimeConfig.maxRequestBodySize
		grpcAppChannelConfig.ReadBufferSize = runtimeConfig.readBufferSize
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
