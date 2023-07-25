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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/actors"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/httpendpoint"
	"github.com/dapr/dapr/pkg/internal/apis"
	"github.com/dapr/dapr/pkg/messaging"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	"github.com/dapr/dapr/pkg/components/pluggable"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
)

const (

	// hot reloading is currently unsupported, but
	// setting this environment variable restores the
	// partial hot reloading support for k8s.
	hotReloadingEnvVar = "DAPR_ENABLE_HOT_RELOADING"

	defaultComponentInitTimeout = time.Second * 5
)

var log = logger.NewLogger("dapr.runtime")

// Type of function that determines if a component is authorized.
// The function receives the component and must return true if the component is authorized.
type ComponentAuthorizer func(component componentsV1alpha1.Component) bool

// Type of function that determines if an http endpoint is authorized.
// The function receives the http endpoint and must return true if the http endpoint is authorized.
type HTTPEndpointAuthorizer func(endpoint httpEndpointV1alpha1.HTTPEndpoint) bool

// DaprRuntime holds all the core components of the runtime.
type DaprRuntime struct {
	runtimeConfig     *internalConfig
	globalConfig      *config.Configuration
	accessControlList *config.AccessControlList
	grpc              *grpc.Manager
	channels          *channels.Channels
	appConfig         config.ApplicationConfig
	directMessaging   messaging.DirectMessaging
	actor             actors.Actors

	nameResolver            nr.Resolver
	hostAddress             string
	actorStateStoreLock     sync.RWMutex
	authenticator           security.Authenticator
	namespace               string
	podName                 string
	daprHTTPAPI             http.API
	daprGRPCAPI             grpc.API
	operatorClient          operatorv1pb.OperatorClient
	shutdownC               chan error
	running                 atomic.Bool
	apiClosers              []io.Closer
	componentAuthorizers    []ComponentAuthorizer
	httpEndpointAuthorizers []HTTPEndpointAuthorizer
	appHealth               *apphealth.AppHealth
	appHealthReady          func() // Invoked the first time the app health becomes ready
	appHealthLock           *sync.Mutex
	compStore               *compstore.ComponentStore
	processor               *processor.Processor
	meta                    *meta.Meta

	pendingHTTPEndpoints       chan httpEndpointV1alpha1.HTTPEndpoint
	pendingComponents          chan componentsV1alpha1.Component
	pendingComponentDependents map[string][]componentsV1alpha1.Component

	proxy messaging.Proxy

	resiliency resiliency.Provider

	tracerProvider *sdktrace.TracerProvider

	workflowEngine *wfengine.WorkflowEngine
}

type componentPreprocessRes struct {
	unreadyDependency string
}

// newDaprRuntime returns a new runtime with the given runtime config and global config.
func newDaprRuntime(ctx context.Context,
	runtimeConfig *internalConfig,
	globalConfig *config.Configuration,
	accessControlList *config.AccessControlList,
	resiliencyProvider resiliency.Provider,
) (*DaprRuntime, error) {
	compStore := compstore.New()
	meta := meta.New(meta.Options{
		ID:        runtimeConfig.id,
		PodName:   getPodName(),
		Namespace: getNamespace(),
		Mode:      runtimeConfig.mode,
	})

	operatorClient, err := getOperatorClient(ctx, runtimeConfig)
	if err != nil {
		return nil, err
	}

	grpc := createGRPCManager(runtimeConfig, globalConfig)

	wfe := wfengine.NewWorkflowEngine(wfengine.NewWorkflowConfig(runtimeConfig.id))
	wfe.ConfigureGrpcExecutor()

	rt := &DaprRuntime{
		runtimeConfig:              runtimeConfig,
		globalConfig:               globalConfig,
		accessControlList:          accessControlList,
		grpc:                       grpc,
		pendingHTTPEndpoints:       make(chan httpEndpointV1alpha1.HTTPEndpoint),
		pendingComponents:          make(chan componentsV1alpha1.Component),
		pendingComponentDependents: map[string][]componentsV1alpha1.Component{},
		shutdownC:                  make(chan error, 1),
		tracerProvider:             nil,
		resiliency:                 resiliencyProvider,
		workflowEngine:             wfe,
		appHealthReady:             nil,
		appHealthLock:              &sync.Mutex{},
		compStore:                  compStore,
		meta:                       meta,
		operatorClient:             operatorClient,
		processor: processor.New(processor.Options{
			ID:               runtimeConfig.id,
			Namespace:        getNamespace(),
			IsHTTP:           runtimeConfig.appConnectionConfig.Protocol.IsHTTP(),
			PlacementEnabled: len(runtimeConfig.placementAddresses) > 0,
			Registry:         runtimeConfig.registry,
			ComponentStore:   compStore,
			Meta:             meta,
			GlobalConfig:     globalConfig,
			Resiliency:       resiliencyProvider,
			Mode:             runtimeConfig.mode,
			PodName:          getPodName(),
			Standalone:       runtimeConfig.standalone,
			OperatorClient:   operatorClient,
			GRPC:             grpc,
		}),
	}

	rt.componentAuthorizers = []ComponentAuthorizer{rt.namespaceComponentAuthorizer}
	if globalConfig != nil && globalConfig.Spec.ComponentsSpec != nil && len(globalConfig.Spec.ComponentsSpec.Deny) > 0 {
		dl := newComponentDenyList(globalConfig.Spec.ComponentsSpec.Deny)
		rt.componentAuthorizers = append(rt.componentAuthorizers, dl.IsAllowed)
	}

	rt.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{rt.namespaceHTTPEndpointAuthorizer}

	return rt, nil
}

// Run performs initialization of the runtime with the runtime and global configurations.
func (a *DaprRuntime) Run(ctx context.Context) error {
	start := time.Now()
	log.Infof("%s mode configured", a.runtimeConfig.mode)
	log.Infof("app id: %s", a.runtimeConfig.id)

	if err := a.initRuntime(ctx); err != nil {
		return err
	}

	a.running.Store(true)

	d := time.Since(start).Milliseconds()
	log.Infof("dapr initialized. Status: Running. Init Elapsed %vms", d)

	if a.daprHTTPAPI != nil {
		// gRPC server start failure is logged as Fatal in initRuntime method. Setting the status only when runtime is initialized.
		a.daprHTTPAPI.MarkStatusAsReady()
	}

	return nil
}

func getNamespace() string {
	return os.Getenv("NAMESPACE")
}

func getPodName() string {
	return os.Getenv("POD_NAME")
}

func getOperatorClient(ctx context.Context, cfg *internalConfig) (operatorv1pb.OperatorClient, error) {
	// Get the operator client only if we're running in Kubernetes and if we need it
	if cfg.mode != modes.KubernetesMode {
		return nil, nil
	}

	client, _, err := client.GetOperatorClient(ctx, cfg.kubernetes.ControlPlaneAddress, security.TLSServerName, cfg.certChain)
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
	a.namespace = getNamespace()

	err := a.establishSecurity(a.runtimeConfig.sentryServiceAddress)
	if err != nil {
		return err
	}
	a.podName = getPodName()

	if a.hostAddress, err = utils.GetHostAddress(); err != nil {
		return fmt.Errorf("failed to determine host address: %w", err)
	}
	if err = a.setupTracing(ctx, a.hostAddress, newOpentelemetryTracerProviderStore()); err != nil {
		return fmt.Errorf("failed to setup tracing: %w", err)
	}
	// Register and initialize name resolution for service discovery.
	err = a.initNameResolution()
	if err != nil {
		log.Errorf(err.Error())
	}

	a.initPluggableComponents(ctx)

	go a.processComponents(ctx)
	go a.processHTTPEndpoints(ctx)

	if _, ok := os.LookupEnv(hotReloadingEnvVar); ok {
		log.Debug("starting to watch component updates")
		err = a.beginComponentsUpdates(ctx)
		if err != nil {
			log.Warnf("failed to watch component updates: %s", err)
		}

		log.Debug("starting to watch http endpoint updates")
		err = a.beginHTTPEndpointsUpdates(ctx)
		if err != nil {
			log.Warnf("failed to watch http endpoint updates: %s", err)
		}
	}

	a.appendBuiltinSecretStore()
	err = a.loadComponents()
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}

	err = a.loadHTTPEndpoints()
	if err != nil {
		log.Warnf("failed to load HTTP endpoints: %s", err)
	}

	a.initChannels()

	a.flushOutstandingComponents()

	pipeline, err := a.channels.BuildHTTPPipeline(a.globalConfig.Spec.HTTPPipelineSpec)
	if err != nil {
		log.Warnf("failed to build HTTP pipeline: %s", err)
	}

	a.flushOutstandingHTTPEndpoints()

	// Setup allow/deny list for secrets
	a.populateSecretsConfiguration()

	// Start proxy
	a.initProxy()

	// Create and start internal and external gRPC servers
	a.daprGRPCAPI = a.getGRPCAPI()

	err = a.startGRPCAPIServer(a.daprGRPCAPI, a.runtimeConfig.apiGRPCPort)
	if err != nil {
		log.Fatalf("failed to start API gRPC server: %s", err)
	}
	if a.runtimeConfig.unixDomainSocket != "" {
		log.Info("API gRPC server is running on a unix domain socket")
	} else {
		log.Infof("API gRPC server is running on port %v", a.runtimeConfig.apiGRPCPort)
	}

	a.initDirectMessaging(a.nameResolver)

	// Start HTTP Server
	err = a.startHTTPServer(a.runtimeConfig.httpPort, a.runtimeConfig.publicPort, a.runtimeConfig.profilePort, a.runtimeConfig.allowedOrigins, pipeline)
	if err != nil {
		log.Fatalf("failed to start HTTP server: %s", err)
	}
	if a.runtimeConfig.unixDomainSocket != "" {
		log.Info("http server is running on a unix domain socket")
	} else {
		log.Infof("http server is running on port %v", a.runtimeConfig.httpPort)
	}
	log.Infof("The request body size parameter is: %v", a.runtimeConfig.maxRequestBodySize)

	err = a.startGRPCInternalServer(a.daprGRPCAPI, a.runtimeConfig.internalGRPCPort)
	if err != nil {
		log.Fatalf("failed to start internal gRPC server: %s", err)
	}
	log.Infof("internal gRPC server is running on port %v", a.runtimeConfig.internalGRPCPort)

	if a.daprHTTPAPI != nil {
		a.daprHTTPAPI.MarkStatusAsOutboundReady()
	}
	a.blockUntilAppIsReady(ctx)

	a.daprHTTPAPI.SetAppChannel(a.channels.AppChannel())
	a.daprGRPCAPI.SetAppChannel(a.channels.AppChannel())
	a.directMessaging.SetAppChannel(a.channels.AppChannel())

	a.directMessaging.SetHTTPEndpointsAppChannels(a.channels.HTTPEndpointsAppChannel(), a.channels.EndpointChannels())

	a.daprHTTPAPI.SetDirectMessaging(a.directMessaging)
	a.daprGRPCAPI.SetDirectMessaging(a.directMessaging)

	if a.runtimeConfig.appConnectionConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.appConnectionConfig.MaxConcurrency)
	}

	a.appHealthReady = func() {
		a.appHealthReadyInit(ctx)
	}
	if a.runtimeConfig.appConnectionConfig.HealthCheck != nil && a.channels.AppChannel() != nil {
		// We can't just pass "a.channels.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.New(*a.runtimeConfig.appConnectionConfig.HealthCheck, func(ctx context.Context) (bool, error) {
			return a.channels.AppChannel().HealthProbe(ctx)
		})
		a.appHealth.OnHealthChange(a.appHealthChanged)
		a.appHealth.StartProbes(ctx)

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
func (a *DaprRuntime) appHealthReadyInit(ctx context.Context) {
	var err error

	// Load app configuration (for actors) and init actors
	a.loadAppConfiguration()

	if len(a.runtimeConfig.placementAddresses) != 0 {
		err = a.initActors()
		if err != nil {
			log.Warn(err)
		} else {
			a.daprHTTPAPI.SetActorRuntime(a.actor)
			a.daprGRPCAPI.SetActorRuntime(a.actor)

			// Workflow engine depends on actor runtime being initialized
			a.initWorkflowEngine(ctx)
		}
	}

	if cb := a.runtimeConfig.registry.ComponentsCallback(); cb != nil {
		if err = cb(registry.ComponentRegistry{
			Actors:          a.actor,
			DirectMessaging: a.directMessaging,
			CompStore:       a.compStore,
		}); err != nil {
			log.Fatalf("failed to register components with callback: %s", err)
		}
	}
}

func (a *DaprRuntime) initWorkflowEngine(ctx context.Context) {
	wfComponentFactory := wfengine.BuiltinWorkflowFactory(a.workflowEngine)

	if wfInitErr := a.workflowEngine.SetActorRuntime(a.actor); wfInitErr != nil {
		log.Warnf("Failed to set actor runtime for Dapr workflow engine - workflow engine will not start: %w", wfInitErr)
	} else {
		if reg := a.runtimeConfig.registry.Workflows(); reg != nil {
			log.Infof("Registering component for dapr workflow engine...")
			reg.RegisterComponent(wfComponentFactory, "dapr")
			if componentInitErr := a.processor.Init(ctx, wfengine.ComponentDefinition); componentInitErr != nil {
				log.Warnf("Failed to initialize Dapr workflow component: %v", componentInitErr)
			}
		} else {
			log.Infof("No workflow registry available, not registering Dapr workflow component...")
		}
	}
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
		// First time the app becomes healthy, complete the init process
		if a.appHealthReady != nil {
			a.appHealthReady()
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
	case apphealth.AppStatusUnhealthy:
		// Stop topic subscriptions and input bindings
		a.processor.PubSub().StopSubscriptions()
		a.processor.Binding().StopReadingFromBindings()
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
		AppChannel:         a.channels.AppChannel(),
		ClientConnFn:       a.grpc.GetGRPCConnection,
		Resolver:           resolver,
		MaxRequestBodySize: a.runtimeConfig.maxRequestBodySize,
		Proxy:              a.proxy,
		ReadBufferSize:     a.runtimeConfig.readBufferSize,
		Resiliency:         a.resiliency,
		IsStreamingEnabled: a.globalConfig.IsFeatureEnabled(config.ServiceInvocationStreaming),
		CompStore:          a.compStore,
	})
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

func (a *DaprRuntime) initChannels() {
	var err error
	a.channels, err = channels.New(channels.Options{
		Registry:            a.runtimeConfig.registry,
		ComponentStore:      a.compStore,
		Meta:                a.meta,
		AppConnectionConfig: a.runtimeConfig.appConnectionConfig,
		GlobalConfig:        a.globalConfig,
		MaxRequestBodySize:  a.runtimeConfig.maxRequestBodySize,
		ReadBufferSize:      a.runtimeConfig.readBufferSize,
		GRPC:                a.grpc,
	})
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.appConnectionConfig.Protocol), err)
	}
	a.processor.SetAppChannel(a.channels.AppChannel())
}

// begin components updates for kubernetes mode.
func (a *DaprRuntime) beginComponentsUpdates(ctx context.Context) error {
	if a.operatorClient == nil {
		return nil
	}

	go func() {
		parseAndUpdate := func(compRaw []byte) {
			var component componentsV1alpha1.Component
			if err := json.Unmarshal(compRaw, &component); err != nil {
				log.Warnf("error deserializing component: %s", err)
				return
			}

			if !a.isObjectAuthorized(component) {
				log.Debugf("received unauthorized component update, ignored. name: %s, type: %s", component.ObjectMeta.Name, component.LogName())
				return
			}

			log.Debugf("received component update. name: %s, type: %s", component.ObjectMeta.Name, component.LogName())
			updated := a.onComponentUpdated(ctx, component)
			if !updated {
				log.Info("component update skipped: .spec field unchanged")
			}
		}

		needList := false
		for {
			var stream operatorv1pb.Operator_ComponentUpdateClient //nolint:nosnakecase

			// Retry on stream error.
			backoff.Retry(func() error {
				var err error
				stream, err = a.operatorClient.ComponentUpdate(context.Background(), &operatorv1pb.ComponentUpdateRequest{
					Namespace: a.namespace,
					PodName:   a.podName,
				})
				if err != nil {
					log.Errorf("error from operator stream: %s", err)
					return err
				}
				return nil
			}, backoff.NewExponentialBackOff())

			if needList {
				// We should get all components again to avoid missing any updates during the failure time.
				backoff.Retry(func() error {
					resp, err := a.operatorClient.ListComponents(context.Background(), &operatorv1pb.ListComponentsRequest{
						Namespace: a.namespace,
					})
					if err != nil {
						log.Errorf("error listing components: %s", err)
						return err
					}

					comps := resp.GetComponents()
					for i := 0; i < len(comps); i++ {
						// avoid missing any updates during the init component time.
						go func(comp []byte) {
							parseAndUpdate(comp)
						}(comps[i])
					}

					return nil
				}, backoff.NewExponentialBackOff())
			}

			for {
				c, err := stream.Recv()
				if err != nil {
					// Retry on stream error.
					needList = true
					log.Errorf("error from operator stream: %s", err)
					break
				}

				parseAndUpdate(c.GetComponent())
			}
		}
	}()
	return nil
}

func (a *DaprRuntime) onComponentUpdated(ctx context.Context, component componentsV1alpha1.Component) bool {
	oldComp, exists := a.compStore.GetComponent(component.Spec.Type, component.Name)
	_, _ = a.processResourceSecrets(ctx, &component)

	if exists && reflect.DeepEqual(oldComp.Spec, component.Spec) {
		return false
	}

	a.pendingComponents <- component
	return true
}

// begin http endpoint updates for kubernetes mode.
func (a *DaprRuntime) beginHTTPEndpointsUpdates(ctx context.Context) error {
	if a.operatorClient == nil {
		return nil
	}

	go func() {
		parseAndUpdate := func(endpointRaw []byte) {
			var endpoint httpEndpointV1alpha1.HTTPEndpoint
			if err := json.Unmarshal(endpointRaw, &endpoint); err != nil {
				log.Warnf("error deserializing http endpoint: %s", err)
				return
			}

			log.Debugf("received http endpoint update for name: %s", endpoint.ObjectMeta.Name)
			updated := a.onHTTPEndpointUpdated(ctx, endpoint)
			if !updated {
				log.Info("http endpoint update skipped: .spec field unchanged")
			}
		}

		needList := false
		for ctx.Err() == nil {
			var stream operatorv1pb.Operator_HTTPEndpointUpdateClient //nolint:nosnakecase
			streamData, err := backoff.RetryWithData(func() (interface{}, error) {
				var err error
				stream, err = a.operatorClient.HTTPEndpointUpdate(context.Background(), &operatorv1pb.HTTPEndpointUpdateRequest{
					Namespace: a.namespace,
					PodName:   a.podName,
				})
				if err != nil {
					log.Errorf("error from operator stream: %s", err)
					return nil, err
				}
				return stream, nil
			}, backoff.NewExponentialBackOff())
			if err != nil {
				// Retry on stream error.
				needList = true
				log.Errorf("error from operator stream: %s", err)
				continue
			}
			stream = streamData.(operatorv1pb.Operator_HTTPEndpointUpdateClient)

			if needList {
				// We should get all http endpoints again to avoid missing any updates during the failure time.
				streamData, err := backoff.RetryWithData(func() (interface{}, error) {
					resp, err := a.operatorClient.ListHTTPEndpoints(context.Background(), &operatorv1pb.ListHTTPEndpointsRequest{
						Namespace: a.namespace,
					})
					if err != nil {
						log.Errorf("error listing http endpoints: %s", err)
						return nil, err
					}

					return resp.GetHttpEndpoints(), nil
				}, backoff.NewExponentialBackOff())
				if err != nil {
					// Retry on stream error.
					log.Errorf("persistent error from operator stream: %s", err)
					continue
				}

				endpointsToUpdate := streamData.([][]byte)
				for i := 0; i < len(endpointsToUpdate); i++ {
					parseAndUpdate(endpointsToUpdate[i])
				}
			}

			for {
				e, err := stream.Recv()
				if err != nil {
					// Retry on stream error.
					needList = true
					log.Errorf("error from operator stream: %s", err)
					break
				}

				parseAndUpdate(e.GetHttpEndpoints())
			}
		}
	}()
	return nil
}

func (a *DaprRuntime) onHTTPEndpointUpdated(ctx context.Context, endpoint httpEndpointV1alpha1.HTTPEndpoint) bool {
	oldEndpoint, exists := a.compStore.GetHTTPEndpoint(endpoint.Name)
	_, _ = a.processResourceSecrets(ctx, &endpoint)

	if exists && reflect.DeepEqual(oldEndpoint.Spec, endpoint.Spec) {
		return false
	}

	a.pendingHTTPEndpoints <- endpoint
	return true
}

func (a *DaprRuntime) processHTTPEndpointSecrets(ctx context.Context, endpoint *httpEndpointV1alpha1.HTTPEndpoint) {
	_, _ = a.processResourceSecrets(ctx, endpoint)

	tlsResource := apis.GenericNameValueResource{
		Name:        endpoint.ObjectMeta.Name,
		Namespace:   endpoint.ObjectMeta.Namespace,
		SecretStore: endpoint.Auth.SecretStore,
		Pairs:       []commonapi.NameValuePair{},
	}

	var root, clientCert, clientKey string = "root", "clientCert", "clientKey"

	ca := commonapi.NameValuePair{
		Name: root,
	}

	if endpoint.HasTLSRootCA() {
		ca.Value = *endpoint.Spec.ClientTLS.RootCA.Value
	}

	if endpoint.HasTLSRootCASecret() {
		ca.SecretKeyRef = *endpoint.Spec.ClientTLS.RootCA.SecretKeyRef
	}
	tlsResource.Pairs = append(tlsResource.Pairs, ca)

	cCert := commonapi.NameValuePair{
		Name: clientCert,
	}

	if endpoint.HasTLSClientCert() {
		cCert.Value = *endpoint.Spec.ClientTLS.Certificate.Value
	}

	if endpoint.HasTLSClientCertSecret() {
		cCert.SecretKeyRef = *endpoint.Spec.ClientTLS.Certificate.SecretKeyRef
	}
	tlsResource.Pairs = append(tlsResource.Pairs, cCert)

	cKey := commonapi.NameValuePair{
		Name: clientKey,
	}

	if endpoint.HasTLSPrivateKey() {
		cKey.Value = *endpoint.Spec.ClientTLS.PrivateKey.Value
	}

	if endpoint.HasTLSPrivateKeySecret() {
		cKey.SecretKeyRef = *endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef
	}

	tlsResource.Pairs = append(tlsResource.Pairs, cKey)

	updated, _ := a.processResourceSecrets(ctx, &tlsResource)
	if updated {
		for _, np := range tlsResource.Pairs {
			dv := &commonapi.DynamicValue{
				JSON: apiextensionsV1.JSON{
					Raw: np.Value.Raw,
				},
			}

			switch np.Name {
			case root:
				endpoint.Spec.ClientTLS.RootCA.Value = dv
			case clientCert:
				endpoint.Spec.ClientTLS.Certificate.Value = dv
			case clientKey:
				endpoint.Spec.ClientTLS.PrivateKey.Value = dv
			}
		}
	}
}

func (a *DaprRuntime) startHTTPServer(port int, publicPort *int, profilePort int, allowedOrigins string, pipeline httpMiddleware.Pipeline) error {
	a.daprHTTPAPI = http.NewAPI(http.APIOpts{
		AppID:                       a.runtimeConfig.id,
		AppChannel:                  a.channels.AppChannel(),
		HTTPEndpointsAppChannel:     a.channels.HTTPEndpointsAppChannel(),
		DirectMessaging:             a.directMessaging,
		Resiliency:                  a.resiliency,
		PubsubAdapter:               a.processor.PubSub(),
		Actors:                      a.actor,
		SendToOutputBindingFn:       a.processor.Binding().SendToOutputBinding,
		TracingSpec:                 a.globalConfig.GetTracingSpec(),
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		MaxRequestBodySize:          int64(a.runtimeConfig.maxRequestBodySize) << 20, // Convert from MB to bytes
		CompStore:                   a.compStore,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
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
		Pipeline:    pipeline,
		APISpec:     a.globalConfig.GetAPISpec(),
	})
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	a.apiClosers = append(a.apiClosers, server)

	return nil
}

func (a *DaprRuntime) startGRPCInternalServer(api grpc.API, port int) error {
	// Since GRPCInteralServer is encrypted & authenticated, it is safe to listen on *
	serverConf := a.getNewServerConfig([]string{""}, port)
	server := grpc.NewInternalServer(api, serverConf, a.globalConfig.GetTracingSpec(), a.globalConfig.GetMetricsSpec(), a.authenticator, a.proxy)
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	a.apiClosers = append(a.apiClosers, server)

	return nil
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := a.getNewServerConfig(a.runtimeConfig.apiListenAddresses, port)
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.GetTracingSpec(), a.globalConfig.GetMetricsSpec(), a.globalConfig.GetAPISpec(), a.proxy, a.workflowEngine)
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	a.apiClosers = append(a.apiClosers, server)

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

func (a *DaprRuntime) getGRPCAPI() grpc.API {
	return grpc.NewAPI(grpc.APIOpts{
		AppID:                       a.runtimeConfig.id,
		AppChannel:                  a.channels.AppChannel(),
		Resiliency:                  a.resiliency,
		PubsubAdapter:               a.processor.PubSub(),
		DirectMessaging:             a.directMessaging,
		Actors:                      a.actor,
		SendToOutputBindingFn:       a.processor.Binding().SendToOutputBinding,
		TracingSpec:                 a.globalConfig.GetTracingSpec(),
		AccessControlList:           a.accessControlList,
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		CompStore:                   a.compStore,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
	})
}

func (a *DaprRuntime) initNameResolution() error {
	var resolver nr.Resolver
	var err error
	resolverMetadata := nr.Metadata{}

	var resolverName, resolverVersion string
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
			fName := utils.ComponentLogName(resolverName, "nameResolution", resolverVersion)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, fmt.Errorf("unable to determine name resolver for %s mode", string(a.runtimeConfig.mode)))
		}
	}

	if resolverVersion == "" {
		resolverVersion = components.FirstStableVersion
	}

	fName := utils.ComponentLogName(resolverName, "nameResolution", resolverVersion)
	resolver, err = a.runtimeConfig.registry.NameResolutions().Create(resolverName, resolverVersion, fName)
	resolverMetadata.Name = resolverName
	if a.globalConfig.Spec.NameResolutionSpec != nil {
		resolverMetadata.Configuration = a.globalConfig.Spec.NameResolutionSpec.Configuration
	}
	resolverMetadata.Properties = map[string]string{
		nr.DaprHTTPPort: strconv.Itoa(a.runtimeConfig.httpPort),
		nr.DaprPort:     strconv.Itoa(a.runtimeConfig.internalGRPCPort),
		nr.AppPort:      strconv.Itoa(a.runtimeConfig.appConnectionConfig.Port),
		nr.HostAddress:  a.hostAddress,
		nr.AppID:        a.runtimeConfig.id,
	}

	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "creation", resolverName)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if err = resolver.Init(resolverMetadata); err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "init", resolverName)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	a.nameResolver = resolver

	log.Infof("Initialized name resolution to %s", resolverName)
	return nil
}

func (a *DaprRuntime) initActors() error {
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
		HostAddress:        a.hostAddress,
		AppID:              a.runtimeConfig.id,
		PlacementAddresses: a.runtimeConfig.placementAddresses,
		Port:               a.runtimeConfig.internalGRPCPort,
		Namespace:          a.namespace,
		AppConfig:          a.appConfig,
		HealthHTTPClient:   a.channels.AppHTTPClient(),
		HealthEndpoint:     a.channels.AppHTTPEndpoint(),
		AppChannelAddress:  a.runtimeConfig.appConnectionConfig.ChannelAddress,
		PodName:            getPodName(),
	})

	act := actors.NewActors(actors.ActorsOpts{
		AppChannel:       a.channels.AppChannel(),
		GRPCConnectionFn: a.grpc.GetGRPCConnection,
		Config:           actorConfig,
		CertChain:        a.runtimeConfig.certChain,
		TracingSpec:      a.globalConfig.GetTracingSpec(),
		Resiliency:       a.resiliency,
		StateStoreName:   actorStateStoreName,
		CompStore:        a.compStore,
		// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
		StateTTLEnabled: a.globalConfig.IsFeatureEnabled(config.ActorStateTTL),
	})
	err = act.Init()
	if err == nil {
		a.actor = act
		return nil
	}
	return rterrors.NewInit(rterrors.InitFailure, "actors", err)
}

func (a *DaprRuntime) namespaceComponentAuthorizer(component componentsV1alpha1.Component) bool {
	if a.namespace == "" || component.ObjectMeta.Namespace == "" || (a.namespace != "" && component.ObjectMeta.Namespace == a.namespace) {
		return component.IsAppScoped(a.runtimeConfig.id)
	}

	return false
}

func (a *DaprRuntime) getAuthorizedObjects(objects interface{}, authorizer func(interface{}) bool) interface{} {
	reflectValue := reflect.ValueOf(objects)
	authorized := reflect.MakeSlice(reflectValue.Type(), 0, reflectValue.Len())
	for i := 0; i < reflectValue.Len(); i++ {
		object := reflectValue.Index(i).Interface()
		if authorizer(object) {
			authorized = reflect.Append(authorized, reflect.ValueOf(object))
		}
	}
	return authorized.Interface()
}

func (a *DaprRuntime) isObjectAuthorized(object interface{}) bool {
	switch obj := object.(type) {
	case httpEndpointV1alpha1.HTTPEndpoint:
		for _, auth := range a.httpEndpointAuthorizers {
			if !auth(obj) {
				return false
			}
		}
	case componentsV1alpha1.Component:
		for _, auth := range a.componentAuthorizers {
			if !auth(obj) {
				return false
			}
		}
	}
	return true
}

func (a *DaprRuntime) namespaceHTTPEndpointAuthorizer(endpoint httpEndpointV1alpha1.HTTPEndpoint) bool {
	switch {
	case a.namespace == "",
		endpoint.ObjectMeta.Namespace == "",
		(a.namespace != "" && endpoint.ObjectMeta.Namespace == a.namespace):
		return endpoint.IsAppScoped(a.runtimeConfig.id)
	default:
		return false
	}
}

func (a *DaprRuntime) loadComponents() error {
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

	authorizedComps := a.getAuthorizedObjects(comps, a.isObjectAuthorized).([]componentsV1alpha1.Component)

	// Iterate through the list twice
	// First, we look for secret stores and load those, then all other components
	// Sure, we could sort the list of authorizedComps... but this is simpler and most certainly faster
	for _, comp := range authorizedComps {
		if strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())
			a.pendingComponents <- comp
		}
	}
	for _, comp := range authorizedComps {
		if !strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())
			a.pendingComponents <- comp
		}
	}

	return nil
}

func (a *DaprRuntime) processComponents(ctx context.Context) {
	for comp := range a.pendingComponents {
		if comp.Name == "" {
			continue
		}

		err := a.processComponentAndDependents(ctx, comp)
		if err != nil {
			e := fmt.Sprintf("process component %s error: %s", comp.Name, err.Error())
			if !comp.Spec.IgnoreErrors {
				log.Warnf("Error processing component, daprd process will exit gracefully")
				a.Shutdown(a.runtimeConfig.gracefulShutdownDuration)
				log.Fatalf(e)
			}
			log.Errorf(e)
		}
	}
}

func (a *DaprRuntime) processHTTPEndpoints(ctx context.Context) {
	for endpoint := range a.pendingHTTPEndpoints {
		if endpoint.Name == "" {
			continue
		}
		a.processHTTPEndpointSecrets(ctx, &endpoint)
		a.compStore.AddHTTPEndpoint(endpoint)
	}
}

func (a *DaprRuntime) flushOutstandingHTTPEndpoints() {
	log.Info("Waiting for all outstanding http endpoints to be processed")
	// We flush by sending a no-op http endpoint. Since the processHTTPEndpoints goroutine only reads one http endpoint at a time,
	// We know that once the no-op http endpoint is read from the channel, all previous http endpoints will have been fully processed.
	a.pendingHTTPEndpoints <- httpEndpointV1alpha1.HTTPEndpoint{}
	log.Info("All outstanding http endpoints processed")
}

func (a *DaprRuntime) flushOutstandingComponents() {
	log.Info("Waiting for all outstanding components to be processed")
	// We flush by sending a no-op component. Since the processComponents goroutine only reads one component at a time,
	// We know that once the no-op component is read from the channel, all previous components will have been fully processed.
	a.pendingComponents <- componentsV1alpha1.Component{}
	log.Info("All outstanding components processed")
}

func (a *DaprRuntime) processComponentAndDependents(ctx context.Context, comp componentsV1alpha1.Component) error {
	log.Debug("Loading component: " + comp.LogName())
	res := a.preprocessOneComponent(ctx, &comp)
	if res.unreadyDependency != "" {
		a.pendingComponentDependents[res.unreadyDependency] = append(a.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := a.processor.Category(comp)
	if compCategory == "" {
		// the category entered is incorrect, return error
		return fmt.Errorf("incorrect type %s", comp.Spec.Type)
	}

	ch := make(chan error, 1)

	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil {
		timeout = defaultComponentInitTimeout
	}

	go func() {
		ch <- a.processor.Init(ctx, comp)
	}()

	select {
	case err := <-ch:
		if err != nil {
			return err
		}
	case <-time.After(timeout):
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		err := fmt.Errorf("init timeout for component %s exceeded after %s", comp.Name, timeout.String())
		return rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), err)
	}

	log.Info("Component loaded: " + comp.LogName())
	diag.DefaultMonitoring.ComponentLoaded()

	dependency := componentDependency(compCategory, comp.Name)
	if deps, ok := a.pendingComponentDependents[dependency]; ok {
		delete(a.pendingComponentDependents, dependency)
		for _, dependent := range deps {
			if err := a.processComponentAndDependents(ctx, dependent); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *DaprRuntime) preprocessOneComponent(ctx context.Context, comp *componentsV1alpha1.Component) componentPreprocessRes {
	_, unreadySecretsStore := a.processResourceSecrets(ctx, comp)
	if unreadySecretsStore != "" {
		return componentPreprocessRes{
			unreadyDependency: componentDependency(components.CategorySecretStore, unreadySecretsStore),
		}
	}
	return componentPreprocessRes{}
}

func (a *DaprRuntime) loadHTTPEndpoints() error {
	var loader httpendpoint.EndpointsLoader

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		loader = httpendpoint.NewKubernetesHTTPEndpoints(a.runtimeConfig.kubernetes, a.namespace, a.operatorClient, a.podName)
	case modes.StandaloneMode:
		loader = httpendpoint.NewLocalHTTPEndpoints(a.runtimeConfig.standalone.ResourcesPath...)
	default:
		return nil
	}

	log.Info("Loading endpoints")
	endpoints, err := loader.LoadHTTPEndpoints()
	if err != nil {
		return err
	}

	authorizedHTTPEndpoints := a.getAuthorizedObjects(endpoints, a.isObjectAuthorized).([]httpEndpointV1alpha1.HTTPEndpoint)

	for _, e := range authorizedHTTPEndpoints {
		log.Infof("Found http endpoint: %s", e.Name)
		a.pendingHTTPEndpoints <- e
	}

	return nil
}

func (a *DaprRuntime) stopActor() {
	if a.actor != nil {
		log.Info("Shutting down actor")
		a.actor.Stop()
	}
}

func (a *DaprRuntime) stopWorkflow(ctx context.Context) {
	if a.workflowEngine != nil {
		log.Info("Shutting down workflow engine")
		a.workflowEngine.Stop(ctx)
	}
}

// shutdownOutputComponents allows for a graceful shutdown of all runtime internal operations of components that are not source of more work.
func (a *DaprRuntime) shutdownOutputComponents(ctx context.Context) error {
	closers := []concurrency.Runner{func(ctx context.Context) error {
		log.Info("Shutting down name resolver")
		if closer, ok := a.nameResolver.(io.Closer); ok && closer != nil {
			if err := closer.Close(); err != nil {
				err = fmt.Errorf("error closing name resolver: %w", err)
				log.Warn(err)
				return err
			}
		}
		return nil
	}}

	closeComponent := func(comp componentsV1alpha1.Component) func(context.Context) error {
		return func(context.Context) error {
			log.Infof("Shutting down component %s", comp.LogName())
			if err := a.processor.Close(comp); err != nil {
				return err
			}
			return nil
		}
	}
	for _, comp := range a.compStore.ListComponents() {
		closers = append(closers, closeComponent(comp))
	}

	return concurrency.NewRunnerManager(closers...).Run(ctx)
}

// ShutdownWithWait will gracefully stop runtime and wait outstanding operations.
func (a *DaprRuntime) ShutdownWithWait() {
	// Another shutdown signal causes an instant shutdown and interrupts the graceful shutdown
	go func() {
		<-ShutdownSignal()
		log.Warn("Received another shutdown signal - shutting down right away")
		os.Exit(0)
	}()

	a.Shutdown(a.runtimeConfig.gracefulShutdownDuration)
	os.Exit(0)
}

func (a *DaprRuntime) cleanSocket() {
	if a.runtimeConfig.unixDomainSocket != "" {
		for _, s := range []string{"http", "grpc"} {
			os.Remove(fmt.Sprintf("%s/dapr-%s-%s.socket", a.runtimeConfig.unixDomainSocket, a.runtimeConfig.id, s))
		}
	}
}

func (a *DaprRuntime) Shutdown(duration time.Duration) {
	// If the shutdown has already been invoked, do nothing
	if !a.running.CompareAndSwap(true, false) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if duration > 0 {
		go func() {
			select {
			case <-time.After(duration):
				log.Fatal("Graceful shutdown timeout exceeded")
			case <-ctx.Done():
				return
			}
		}()
	}

	// Ensure the Unix socket file is removed if a panic occurs.
	defer a.cleanSocket()
	log.Info("Dapr shutting down")
	log.Info("Stopping input bindings")
	a.processor.Binding().StopReadingFromBindings()

	// shutdown workflow if it's running
	a.stopWorkflow(ctx)

	log.Info("Initiating actor shutdown")
	a.stopActor()

	if a.appHealth != nil {
		log.Info("Closing App Health")
		a.appHealth.Close()
	}

	log.Info("Stopping Dapr APIs")
	for _, closer := range a.apiClosers {
		if err := closer.Close(); err != nil {
			log.Warnf("Error closing API: %v", err)
		}
	}
	a.stopTrace(ctx)
	if err := a.shutdownOutputComponents(ctx); err != nil {
		log.Warnf("Error shutting down components: %v", err)
	}
	log.Info("Dapr runtime stopped successfully")
	a.shutdownC <- nil
}

// SetRunning updates the value of the running flag.
// This method is used by tests in dapr/components-contrib.
func (a *DaprRuntime) SetRunning(val bool) {
	a.running.Store(val)
}

// WaitUntilShutdown waits until the Shutdown() method is done.
// This method is used by tests in dapr/components-contrib.
func (a *DaprRuntime) WaitUntilShutdown() error {
	return <-a.shutdownC
}

func isEnvVarAllowed(key string) bool {
	// First, apply a denylist that blocks access to sensitive env vars
	key = strings.ToUpper(key)
	switch {
	case key == "":
		return false
	case key == "APP_API_TOKEN":
		return false
	case strings.HasPrefix(key, "DAPR_"):
		return false
	case strings.Contains(key, " "):
		return false
	}

	// If we have a `DAPR_ENV_KEYS` env var (which is added by the Dapr Injector in Kubernetes mode), use that as allowlist too
	allowlist := os.Getenv(authConsts.EnvKeysEnvVar)
	if allowlist == "" {
		return true
	}

	// Need to check for the full var, so there must be a space after OR it must be the end of the string, and there must be a space before OR it must be at the beginning of the string
	idx := strings.Index(allowlist, key)
	if idx >= 0 &&
		(idx+len(key) == len(allowlist) || allowlist[idx+len(key)] == ' ') &&
		(idx == 0 || allowlist[idx-1] == ' ') {
		return true
	}
	return false
}

// Returns the component or HTTP endpoint updated with the secrets applied.
// If the resource references a secret store that hasn't been loaded yet, it returns the name of the secret store component as second returned value.
func (a *DaprRuntime) processResourceSecrets(ctx context.Context, resource meta.Resource) (updated bool, secretStoreName string) {
	cache := map[string]secretstores.GetSecretResponse{}

	secretStoreName = a.authSecretStoreOrDefault(resource)

	metadata := resource.NameValuePairs()
	for i, m := range metadata {
		// If there's an env var and no value, use that
		if !m.HasValue() && m.EnvRef != "" {
			if isEnvVarAllowed(m.EnvRef) {
				metadata[i].SetValue([]byte(os.Getenv(m.EnvRef)))
			} else {
				log.Warnf("%s %s references an env variable that isn't allowed: %s", resource.Kind(), resource.GetName(), m.EnvRef)
			}
			metadata[i].EnvRef = ""
			updated = true
			continue
		}

		if m.SecretKeyRef.Name == "" {
			continue
		}

		// If running in Kubernetes and have an operator client, do not fetch secrets from the Kubernetes secret store as they will be populated by the operator.
		// Instead, base64 decode the secret values into their real self.
		if a.operatorClient != nil && secretStoreName == secretstoresLoader.BuiltinKubernetesSecretStore {
			var jsonVal string
			err := json.Unmarshal(m.Value.Raw, &jsonVal)
			if err != nil {
				log.Errorf("Error decoding secret: %v", err)
				continue
			}

			dec, err := base64.StdEncoding.DecodeString(jsonVal)
			if err != nil {
				log.Errorf("Error decoding secret: %v", err)
				continue
			}

			metadata[i].SetValue(dec)
			metadata[i].SecretKeyRef = commonapi.SecretKeyRef{}
			updated = true
			continue
		}

		secretStore, ok := a.compStore.GetSecretStore(secretStoreName)
		if !ok {
			log.Warnf("%s %s references a secret store that isn't loaded: %s", resource.Kind(), resource.GetName(), secretStoreName)
			return updated, secretStoreName
		}

		resp, ok := cache[m.SecretKeyRef.Name]
		if !ok {
			r, err := secretStore.GetSecret(ctx, secretstores.GetSecretRequest{
				Name: m.SecretKeyRef.Name,
				Metadata: map[string]string{
					"namespace": resource.GetNamespace(),
				},
			})
			if err != nil {
				log.Errorf("Error getting secret: %v", err)
				continue
			}
			resp = r
		}

		// Use the SecretKeyRef.Name key if SecretKeyRef.Key is not given
		secretKeyName := m.SecretKeyRef.Key
		if secretKeyName == "" {
			secretKeyName = m.SecretKeyRef.Name
		}

		val, ok := resp.Data[secretKeyName]
		if ok && val != "" {
			metadata[i].SetValue([]byte(val))
			metadata[i].SecretKeyRef = commonapi.SecretKeyRef{}
			updated = true
		}

		cache[m.SecretKeyRef.Name] = resp
	}
	return updated, ""
}

func (a *DaprRuntime) authSecretStoreOrDefault(resource meta.Resource) string {
	secretStore := resource.GetSecretStore()
	if secretStore == "" {
		switch a.runtimeConfig.mode {
		case modes.KubernetesMode:
			return "kubernetes"
		}
	}
	return secretStore
}

func (a *DaprRuntime) blockUntilAppIsReady(ctx context.Context) {
	if a.runtimeConfig.appConnectionConfig.Port <= 0 {
		return
	}

	log.Infof("application protocol: %s. waiting on port %v.  This will block until the app is listening on that port.", string(a.runtimeConfig.appConnectionConfig.Protocol), a.runtimeConfig.appConnectionConfig.Port)

	dialAddr := a.runtimeConfig.appConnectionConfig.ChannelAddress + ":" + strconv.Itoa(a.runtimeConfig.appConnectionConfig.Port)

	stopCh := ShutdownSignal()
	defer signal.Stop(stopCh)
	for {
		select {
		case <-stopCh:
			// Cause a shutdown
			a.ShutdownWithWait()
			return
		case <-ctx.Done():
			// Return
			return
		default:
			// nop - continue execution
		}

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
			conn, err = dialer.Dial("tcp", dialAddr)
		}
		if err == nil && conn != nil {
			conn.Close()
			break
		}
		// prevents overwhelming the OS with open connections
		time.Sleep(time.Millisecond * 100)
	}

	log.Infof("application discovered on port %v", a.runtimeConfig.appConnectionConfig.Port)
}

func (a *DaprRuntime) loadAppConfiguration() {
	if a.channels.AppChannel() == nil {
		return
	}

	appConfig, err := a.channels.AppChannel().GetAppConfig(a.runtimeConfig.id)
	if err != nil {
		return
	}

	if appConfig != nil {
		a.appConfig = *appConfig
		log.Info("Application configuration loaded")
	}
}

func (a *DaprRuntime) appendBuiltinSecretStore() {
	if a.runtimeConfig.disableBuiltinK8sSecretStore {
		return
	}

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		// Preload Kubernetes secretstore
		a.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: components.FirstStableVersion,
			},
		}
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

func (a *DaprRuntime) establishSecurity(sentryAddress string) error {
	if !a.runtimeConfig.mTLSEnabled {
		log.Info("mTLS is disabled. Skipping certificate request and tls validation")
		return nil
	}
	if sentryAddress == "" {
		return errors.New("sentryAddress cannot be empty")
	}
	log.Info("mTLS enabled. creating sidecar authenticator")

	auth, err := security.GetSidecarAuthenticator(sentryAddress, a.runtimeConfig.certChain)
	if err != nil {
		return err
	}
	a.authenticator = auth
	a.grpc.SetAuthenticator(auth)

	log.Info("authenticator created")

	diag.DefaultMonitoring.MTLSInitCompleted()
	return nil
}

func componentDependency(compCategory components.Category, name string) string {
	return fmt.Sprintf("%s:%s", compCategory, name)
}

func createGRPCManager(runtimeConfig *internalConfig, globalConfig *config.Configuration) *grpc.Manager {
	grpcAppChannelConfig := &grpc.AppChannelConfig{}
	if globalConfig != nil {
		grpcAppChannelConfig.TracingSpec = globalConfig.GetTracingSpec()
		grpcAppChannelConfig.AllowInsecureTLS = globalConfig.IsFeatureEnabled(config.AppChannelAllowInsecureTLS)
	}
	if runtimeConfig != nil {
		grpcAppChannelConfig.Port = runtimeConfig.appConnectionConfig.Port
		grpcAppChannelConfig.MaxConcurrency = runtimeConfig.appConnectionConfig.MaxConcurrency
		grpcAppChannelConfig.EnableTLS = (runtimeConfig.appConnectionConfig.Protocol == protocol.GRPCSProtocol)
		grpcAppChannelConfig.MaxRequestBodySizeMB = runtimeConfig.maxRequestBodySize
		grpcAppChannelConfig.ReadBufferSizeKB = runtimeConfig.readBufferSize
		grpcAppChannelConfig.BaseAddress = runtimeConfig.appConnectionConfig.ChannelAddress
	}

	m := grpc.NewGRPCManager(runtimeConfig.mode, grpcAppChannelConfig)
	m.StartCollector()
	return m
}

func (a *DaprRuntime) stopTrace(ctx context.Context) {
	if a.tracerProvider == nil {
		return
	}
	// Flush and shutdown the tracing provider.
	if err := a.tracerProvider.ForceFlush(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warnf("error flushing tracing provider: %v", err)
	}

	if err := a.tracerProvider.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warnf("error shutting down tracing provider: %v", err)
	} else {
		a.tracerProvider = nil
	}
}

// ShutdownSignal returns a signal that receives a message when the app needs to shut down
func ShutdownSignal() chan os.Signal {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	return stop
}
