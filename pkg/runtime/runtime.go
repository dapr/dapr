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
	nethttp "net/http"
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
	"go.opentelemetry.io/otel/trace"
	md "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/actors"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/httpendpoint"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/processor/binding"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"github.com/dapr/dapr/pkg/components/pluggable"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"

	"github.com/dapr/components-contrib/bindings"
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
	runtimeConfig           *internalConfig
	globalConfig            *config.Configuration
	accessControlList       *config.AccessControlList
	grpc                    *grpc.Manager
	channels                *channels.Channels
	httpEndpointsAppChannel channel.HTTPEndpointAppChannel // extra app channel to allow for different URLs per call.
	appConfig               config.ApplicationConfig
	directMessaging         messaging.DirectMessaging
	actor                   actors.Actors
	subscribeBindingList    []string

	nameResolver            nr.Resolver
	hostAddress             string
	actorStateStoreLock     sync.RWMutex
	authenticator           security.Authenticator
	namespace               string
	podName                 string
	daprHTTPAPI             http.API
	daprGRPCAPI             grpc.API
	operatorClient          operatorv1pb.OperatorClient
	pubsubCancel            context.CancelFunc
	shutdownC               chan error
	running                 atomic.Bool
	apiClosers              []io.Closer
	componentAuthorizers    []ComponentAuthorizer
	httpEndpointAuthorizers []HTTPEndpointAuthorizer
	appHealth               *apphealth.AppHealth
	appHealthReady          func() // Invoked the first time the app health becomes ready
	appHealthLock           *sync.Mutex
	appHTTPClient           *nethttp.Client
	compStore               *compstore.ComponentStore
	processor               *processor.Processor
	meta                    *meta.Meta

	inputBindingsCtx    context.Context
	inputBindingsCancel context.CancelFunc

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

type pubsubSubscribedMessage struct {
	cloudEvent map[string]interface{}
	data       []byte
	topic      string
	metadata   map[string]string
	path       string
	pubsub     string
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
		return nil, fmt.Errorf("error creating operator client: %w", err)
	}

	grpc := createGRPCManager(runtimeConfig, globalConfig)

	channels, err := channels.New(channels.Options{
		Registry:            runtimeConfig.registry,
		ComponentStore:      compStore,
		Meta:                meta,
		AppConnectionConfig: runtimeConfig.appConnectionConfig,
		GlobalConfig:        globalConfig,
		MaxRequestBodySize:  runtimeConfig.maxRequestBodySize,
		ReadBufferSize:      runtimeConfig.readBufferSize,
		GRPC:                grpc,
	})
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(runtimeConfig.appConnectionConfig.Protocol), err)
	}

	rt := &DaprRuntime{
		channels:                   channels,
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
		workflowEngine:             wfengine.NewWorkflowEngine(wfengine.NewWorkflowConfig(runtimeConfig.id)),
		appHealthReady:             nil,
		appHealthLock:              &sync.Mutex{},
		compStore:                  compStore,
		meta:                       meta,
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
			AppChannel:       channels.AppChannel(),
			OperatorClient:   operatorClient,
			GRPC:             grpc,
		}),
	}

	rt.componentAuthorizers = []ComponentAuthorizer{rt.namespaceComponentAuthorizer}
	if globalConfig != nil && len(globalConfig.Spec.ComponentsSpec.Deny) > 0 {
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
func (a *DaprRuntime) setupTracing(hostAddress string, tpStore tracerProviderStore) error {
	tracingSpec := a.globalConfig.Spec.TracingSpec

	// Register stdout trace exporter if user wants to debug requests or log as Info level.
	if tracingSpec.Stdout {
		tpStore.RegisterExporter(diagUtils.NewStdOutExporter())
	}

	// Register zipkin trace exporter if ZipkinSpec is specified
	if tracingSpec.Zipkin.EndpointAddress != "" {
		zipkinExporter, err := zipkin.New(tracingSpec.Zipkin.EndpointAddress)
		if err != nil {
			return err
		}
		tpStore.RegisterExporter(zipkinExporter)
	}

	// Register otel trace exporter if OtelSpec is specified
	if tracingSpec.Otel.EndpointAddress != "" && tracingSpec.Otel.Protocol != "" {
		endpoint := tracingSpec.Otel.EndpointAddress
		protocol := tracingSpec.Otel.Protocol
		if protocol != "http" && protocol != "grpc" {
			return fmt.Errorf("invalid protocol %v provided for Otel endpoint", protocol)
		}
		isSecure := tracingSpec.Otel.IsSecure

		var client otlptrace.Client
		if protocol == "http" {
			clientOptions := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
			if !isSecure {
				clientOptions = append(clientOptions, otlptracehttp.WithInsecure())
			}
			client = otlptracehttp.NewClient(clientOptions...)
		} else {
			clientOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
			if !isSecure {
				clientOptions = append(clientOptions, otlptracegrpc.WithInsecure())
			}
			client = otlptracegrpc.NewClient(clientOptions...)
		}
		// TODO: @joshvanl
		otelExporter, err := otlptrace.New(context.TODO(), client)
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
	a.operatorClient, err = getOperatorClient(ctx, a.runtimeConfig)
	if err != nil {
		return err
	}

	if a.hostAddress, err = utils.GetHostAddress(); err != nil {
		return fmt.Errorf("failed to determine host address: %w", err)
	}
	if err = a.setupTracing(a.hostAddress, newOpentelemetryTracerProviderStore()); err != nil {
		return fmt.Errorf("failed to setup tracing: %w", err)
	}
	// Register and initialize name resolution for service discovery.
	err = a.initNameResolution()
	if err != nil {
		log.Errorf(err.Error())
	}

	a.initPluggableComponents()

	go a.processComponents()
	go a.processHTTPEndpoints()

	if _, ok := os.LookupEnv(hotReloadingEnvVar); ok {
		log.Debug("starting to watch component updates")
		err = a.beginComponentsUpdates()
		if err != nil {
			log.Warnf("failed to watch component updates: %s", err)
		}

		log.Debug("starting to watch http endpoint updates")
		err = a.beginHTTPEndpointsUpdates()
		if err != nil {
			log.Warnf("failed to watch http endpoint updates: %s", err)
		}
	}

	a.appendBuiltinSecretStore()
	err = a.loadComponents()
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}

	a.flushOutstandingComponents()

	pipeline, err := a.channels.BuildHTTPPipeline(a.globalConfig.Spec.HTTPPipelineSpec)
	if err != nil {
		log.Warnf("failed to build HTTP pipeline: %s", err)
	}

	err = a.loadHTTPEndpoints()
	if err != nil {
		log.Warnf("failed to load HTTP endpoints: %s", err)
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
	a.blockUntilAppIsReady()

	a.daprHTTPAPI.SetAppChannel(a.channels.AppChannel())
	a.daprGRPCAPI.SetAppChannel(a.channels.AppChannel())
	a.directMessaging.SetAppChannel(a.channels.AppChannel())

	// add another app channel dedicated to external service invocation
	a.daprHTTPAPI.SetHTTPEndpointsAppChannel(a.httpEndpointsAppChannel)
	a.directMessaging.SetHTTPEndpointsAppChannel(a.httpEndpointsAppChannel)

	a.daprHTTPAPI.SetDirectMessaging(a.directMessaging)
	a.daprGRPCAPI.SetDirectMessaging(a.directMessaging)

	if a.runtimeConfig.appConnectionConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.appConnectionConfig.MaxConcurrency)
	}

	a.appHealthReady = func() {
		a.appHealthReadyInit(ctx)
	}
	if a.runtimeConfig.appConnectionConfig.HealthCheck != nil && a.channels.AppChannel() != nil {
		// We can't just pass "a.appChannel.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.New(*a.runtimeConfig.appConnectionConfig.HealthCheck, func(ctx context.Context) (bool, error) {
			return a.channels.AppChannel().HealthProbe(ctx)
		})
		a.appHealth.OnHealthChange(a.appHealthChanged)
		// TODO: @joshvanl
		a.appHealth.StartProbes(context.TODO())

		// Set the appHealth object in the channel so it's aware of the app's health status
		a.channels.AppChannel().SetAppHealth(a.appHealth)

		// Enqueue a probe right away
		// This will also start the input components once the app is healthy
		a.appHealth.Enqueue()
	} else {
		// If there's no health check, mark the app as healthy right away so subscriptions can start
		a.appHealthChanged(apphealth.AppStatusHealthy)
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
func (a *DaprRuntime) initPluggableComponents() {
	if runtime.GOOS == "windows" {
		log.Debugf("the current OS does not support pluggable components feature, skipping initialization")
		return
	}
	// TODO: @joshvanl
	if err := pluggable.Discover(context.TODO()); err != nil {
		log.Errorf("could not initialize pluggable components %v", err)
	}
}

// Sets the status of the app to healthy or un-healthy
// Callback for apphealth when the detected status changed
func (a *DaprRuntime) appHealthChanged(status uint8) {
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
		a.processor.PubSub().StartSubscriptions(context.TODO())
		err := a.startReadingFromBindings()
		if err != nil {
			log.Warnf("failed to read from bindings: %s ", err)
		}
	case apphealth.AppStatusUnhealthy:
		// Stop topic subscriptions and input bindings
		a.processor.PubSub().StopSubscriptions()
		a.stopReadingFromBindings()
	}
}

func (a *DaprRuntime) populateSecretsConfiguration() {
	// Populate in a map for easy lookup by store name.
	for _, scope := range a.globalConfig.Spec.Secrets.Scopes {
		a.compStore.AddSecretsConfiguration(scope.StoreName, scope)
	}
}

func (a *DaprRuntime) initDirectMessaging(resolver nr.Resolver) {
	a.directMessaging = messaging.NewDirectMessaging(messaging.NewDirectMessagingOpts{
		AppID:                   a.runtimeConfig.id,
		Namespace:               a.namespace,
		Port:                    a.runtimeConfig.internalGRPCPort,
		Mode:                    a.runtimeConfig.mode,
		AppChannel:              a.channels.AppChannel(),
		HTTPEndpointsAppChannel: a.httpEndpointsAppChannel,
		ClientConnFn:            a.grpc.GetGRPCConnection,
		Resolver:                resolver,
		MaxRequestBodySize:      a.runtimeConfig.maxRequestBodySize,
		Proxy:                   a.proxy,
		ReadBufferSize:          a.runtimeConfig.readBufferSize,
		Resiliency:              a.resiliency,
		IsStreamingEnabled:      a.globalConfig.IsFeatureEnabled(config.ServiceInvocationStreaming),
		CompStore:               a.compStore,
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

// begin components updates for kubernetes mode.
func (a *DaprRuntime) beginComponentsUpdates() error {
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
			updated := a.onComponentUpdated(component)
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

func (a *DaprRuntime) onComponentUpdated(component componentsV1alpha1.Component) bool {
	oldComp, exists := a.compStore.GetComponent(component.Spec.Type, component.Name)
	_, _ = a.processResourceSecrets(&component)

	if exists && reflect.DeepEqual(oldComp.Spec, component.Spec) {
		return false
	}

	a.pendingComponents <- component
	return true
}

// begin http endpoint updates for kubernetes mode.
func (a *DaprRuntime) beginHTTPEndpointsUpdates() error {
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
			updated := a.onHTTPEndpointUpdated(endpoint)
			if !updated {
				log.Info("http endpoint update skipped: .spec field unchanged")
			}
		}

		needList := false
		// TODO: @joshvanl
		//for a.ctx.Err() == nil {
		for {
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

func (a *DaprRuntime) onHTTPEndpointUpdated(endpoint httpEndpointV1alpha1.HTTPEndpoint) bool {
	oldEndpoint, exists := a.compStore.GetHTTPEndpoint(endpoint.Name)
	_, _ = a.processResourceSecrets(&endpoint)

	if exists && reflect.DeepEqual(oldEndpoint.Spec, endpoint.Spec) {
		return false
	}

	a.pendingHTTPEndpoints <- endpoint
	return true
}

func (a *DaprRuntime) sendBatchOutputBindingsParallel(to []string, data []byte) {
	for _, dst := range to {
		go func(name string) {
			_, err := a.sendToOutputBinding(name, &bindings.InvokeRequest{
				Data:      data,
				Operation: bindings.CreateOperation,
			})
			if err != nil {
				log.Error(err)
			}
		}(dst)
	}
}

func (a *DaprRuntime) sendBatchOutputBindingsSequential(to []string, data []byte) error {
	for _, dst := range to {
		_, err := a.sendToOutputBinding(dst, &bindings.InvokeRequest{
			Data:      data,
			Operation: bindings.CreateOperation,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *DaprRuntime) sendToOutputBinding(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	if req.Operation == "" {
		return nil, errors.New("operation field is missing from request")
	}

	if binding, ok := a.compStore.GetOutputBinding(name); ok {
		ops := binding.Operations()
		for _, o := range ops {
			if o == req.Operation {
				//policyRunner := resiliency.NewRunner[*bindings.InvokeResponse](a.ctx,
				// TODO: @joshvanl
				policyRunner := resiliency.NewRunner[*bindings.InvokeResponse](context.TODO(),
					a.resiliency.ComponentOutboundPolicy(name, resiliency.Binding),
				)
				return policyRunner(func(ctx context.Context) (*bindings.InvokeResponse, error) {
					return binding.Invoke(ctx, req)
				})
			}
		}
		supported := make([]string, 0, len(ops))
		for _, o := range ops {
			supported = append(supported, string(o))
		}
		return nil, fmt.Errorf("binding %s does not support operation %s. supported operations:%s", name, req.Operation, strings.Join(supported, " "))
	}
	return nil, fmt.Errorf("couldn't find output binding %s", name)
}

func (a *DaprRuntime) onAppResponse(response *bindings.AppResponse) error {
	if len(response.State) > 0 {
		go func(reqs []state.SetRequest) {
			store, ok := a.compStore.GetStateStore(response.StoreName)
			if !ok {
				return
			}

			// TODO: @joshvanl
			//err := stateLoader.PerformBulkStoreOperation(a.ctx, reqs,
			err := stateLoader.PerformBulkStoreOperation(context.TODO(), reqs,
				a.resiliency.ComponentOutboundPolicy(response.StoreName, resiliency.Statestore),
				state.BulkStoreOpts{},
				store.Set,
				store.BulkSet,
			)
			if err != nil {
				log.Errorf("error saving state from app response: %v", err)
			}
		}(response.State)
	}

	if len(response.To) > 0 {
		b, err := json.Marshal(&response.Data)
		if err != nil {
			return err
		}

		if response.Concurrency == binding.ConcurrencyParallel {
			a.sendBatchOutputBindingsParallel(response.To, b)
		} else {
			return a.sendBatchOutputBindingsSequential(response.To, b)
		}
	}

	return nil
}

func (a *DaprRuntime) sendBindingEventToApp(bindingName string, data []byte, metadata map[string]string) ([]byte, error) {
	var response bindings.AppResponse
	spanName := "bindings/" + bindingName
	spanContext := trace.SpanContext{}

	// Check the grpc-trace-bin with fallback to traceparent.
	validTraceparent := false
	if val, ok := metadata[diag.GRPCTraceContextKey]; ok {
		if sc, ok := diagUtils.SpanContextFromBinary([]byte(val)); ok {
			spanContext = sc
		}
	} else if val, ok := metadata[diag.TraceparentHeader]; ok {
		if sc, ok := diag.SpanContextFromW3CString(val); ok {
			spanContext = sc
			validTraceparent = true
			// Only parse the tracestate if we've successfully parsed the traceparent.
			if val, ok := metadata[diag.TracestateHeader]; ok {
				ts := diag.TraceStateFromW3CString(val)
				spanContext.WithTraceState(*ts)
			}
		}
	}
	// span is nil if tracing is disabled (sampling rate is 0)
	// TODO: @joshvanl
	ctx, span := diag.StartInternalCallbackSpan(context.TODO(), spanName, spanContext, a.globalConfig.Spec.TracingSpec)

	var appResponseBody []byte
	path, _ := a.compStore.GetInputBindingRoute(bindingName)
	if path == "" {
		path = bindingName
	}

	if !a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
		if span != nil {
			ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
		}

		// Add workaround to fallback on checking traceparent header.
		// As grpc-trace-bin is not yet there in OpenTelemetry unlike OpenCensus, tracking issue https://github.com/open-telemetry/opentelemetry-specification/issues/639
		// and grpc-dotnet client adheres to OpenTelemetry Spec which only supports http based traceparent header in gRPC path.
		// TODO: Remove this workaround fix once grpc-dotnet supports grpc-trace-bin header. Tracking issue https://github.com/dapr/dapr/issues/1827.
		if validTraceparent && span != nil {
			spanContextHeaders := make(map[string]string, 2)
			diag.SpanContextToHTTPHeaders(span.SpanContext(), func(key string, val string) {
				spanContextHeaders[key] = val
			})
			for key, val := range spanContextHeaders {
				ctx = md.AppendToOutgoingContext(ctx, key, val)
			}
		}

		conn, err := a.grpc.GetAppClient()
		defer a.grpc.ReleaseAppClient(conn)
		if err != nil {
			return nil, fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		req := &runtimev1pb.BindingEventRequest{
			Name:     bindingName,
			Data:     data,
			Metadata: metadata,
		}
		start := time.Now()

		policyRunner := resiliency.NewRunner[*runtimev1pb.BindingEventResponse](ctx,
			a.resiliency.ComponentInboundPolicy(bindingName, resiliency.Binding),
		)
		resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.BindingEventResponse, error) {
			return client.OnBindingEvent(ctx, req)
		})

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				"/dapr.proto.runtime.v1.AppCallback/OnBindingEvent")
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromGRPCError(span, err)
			span.End()
		}
		if diag.DefaultGRPCMonitoring.IsEnabled() {
			diag.DefaultGRPCMonitoring.ServerRequestSent(ctx,
				"/dapr.proto.runtime.v1.AppCallback/OnBindingEvent",
				status.Code(err).String(),
				int64(len(resp.GetData())), start)
		}

		if err != nil {
			return nil, fmt.Errorf("error invoking app: %w", err)
		}
		if resp != nil {
			if resp.Concurrency == runtimev1pb.BindingEventResponse_PARALLEL { //nolint:nosnakecase
				response.Concurrency = binding.ConcurrencyParallel
			} else {
				response.Concurrency = binding.ConcurrencySequential
			}

			response.To = resp.To

			if resp.Data != nil {
				appResponseBody = resp.Data

				var d interface{}
				err := json.Unmarshal(resp.Data, &d)
				if err == nil {
					response.Data = d
				}
			}
		}
	} else {
		policyDef := a.resiliency.ComponentInboundPolicy(bindingName, resiliency.Binding)

		reqMetadata := make(map[string][]string, len(metadata))
		for k, v := range metadata {
			reqMetadata[k] = []string{v}
		}
		req := invokev1.NewInvokeMethodRequest(path).
			WithHTTPExtension(nethttp.MethodPost, "").
			WithRawDataBytes(data).
			WithContentType(invokev1.JSONContentType).
			WithMetadata(reqMetadata)
		if policyDef != nil {
			req.WithReplay(policyDef.HasRetries())
		}
		defer req.Close()

		respErr := errors.New("error sending binding event to application")
		policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
			resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
				Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
			},
		)
		resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
			rResp, rErr := a.channels.AppChannel().InvokeMethod(ctx, req, "")
			if rErr != nil {
				return rResp, rErr
			}
			if rResp != nil && rResp.Status().Code != nethttp.StatusOK {
				return rResp, fmt.Errorf("%w, status %d", respErr, rResp.Status().Code)
			}
			return rResp, nil
		})
		if err != nil && !errors.Is(err, respErr) {
			return nil, fmt.Errorf("error invoking app: %w", err)
		}

		if resp == nil {
			return nil, errors.New("error invoking app: response object is nil")
		}
		defer resp.Close()

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				nethttp.MethodPost+" /"+bindingName,
			)
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, int(resp.Status().Code))
			span.End()
		}

		appResponseBody, err = resp.RawDataFull()

		// ::TODO report metrics for http, such as grpc
		if resp.Status().Code < 200 || resp.Status().Code > 299 {
			return nil, fmt.Errorf("fails to send binding event to http app channel, status code: %d body: %s", resp.Status().Code, string(appResponseBody))
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}

	if len(response.State) > 0 || len(response.To) > 0 {
		if err := a.onAppResponse(&response); err != nil {
			log.Errorf("error executing app response: %s", err)
		}
	}

	return appResponseBody, nil
}

func (a *DaprRuntime) readFromBinding(readCtx context.Context, name string, binding bindings.InputBinding) error {
	return binding.Read(readCtx, func(_ context.Context, resp *bindings.ReadResponse) ([]byte, error) {
		if resp == nil {
			return nil, nil
		}

		start := time.Now()
		b, err := a.sendBindingEventToApp(name, resp.Data, resp.Metadata)
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.InputBindingEvent(context.Background(), name, err == nil, elapsed)

		if err != nil {
			log.Debugf("error from app consumer for binding [%s]: %s", name, err)
			return nil, err
		}
		return b, nil
	})
}

func (a *DaprRuntime) startHTTPServer(port int, publicPort *int, profilePort int, allowedOrigins string, pipeline httpMiddleware.Pipeline) error {
	a.daprHTTPAPI = http.NewAPI(http.APIOpts{
		AppID:                       a.runtimeConfig.id,
		AppChannel:                  a.channels.AppChannel(),
		HTTPEndpointsAppChannel:     a.httpEndpointsAppChannel,
		DirectMessaging:             a.directMessaging,
		Resiliency:                  a.resiliency,
		PubsubAdapter:               a.processor.PubSub(),
		Actors:                      a.actor,
		SendToOutputBindingFn:       a.sendToOutputBinding,
		TracingSpec:                 a.globalConfig.Spec.TracingSpec,
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
		APILoggingObfuscateURLs: a.globalConfig.Spec.LoggingSpec.APILogging.ObfuscateURLs,
		APILogHealthChecks:      !a.globalConfig.Spec.LoggingSpec.APILogging.OmitHealthChecks,
	}

	server := http.NewServer(http.NewServerOpts{
		API:         a.daprHTTPAPI,
		Config:      serverConf,
		TracingSpec: a.globalConfig.Spec.TracingSpec,
		MetricSpec:  a.globalConfig.Spec.MetricSpec,
		Pipeline:    pipeline,
		APISpec:     a.globalConfig.Spec.APISpec,
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
	server := grpc.NewInternalServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, a.authenticator, a.proxy)
	if err := server.StartNonBlocking(); err != nil {
		return err
	}
	a.apiClosers = append(a.apiClosers, server)

	return nil
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := a.getNewServerConfig(a.runtimeConfig.apiListenAddresses, port)
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, a.globalConfig.Spec.APISpec, a.proxy, a.workflowEngine)
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
		SendToOutputBindingFn:       a.sendToOutputBinding,
		TracingSpec:                 a.globalConfig.Spec.TracingSpec,
		AccessControlList:           a.accessControlList,
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		CompStore:                   a.compStore,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
	})
}

func (a *DaprRuntime) getSubscribedBindingsGRPC() ([]string, error) {
	conn, err := a.grpc.GetAppClient()
	defer a.grpc.ReleaseAppClient(conn)
	if err != nil {
		return nil, fmt.Errorf("error while getting app client: %w", err)
	}
	client := runtimev1pb.NewAppCallbackClient(conn)
	resp, err := client.ListInputBindings(context.Background(), &emptypb.Empty{})
	bindings := []string{}

	if err == nil && resp != nil {
		bindings = resp.Bindings
	}
	return bindings, nil
}

func (a *DaprRuntime) isAppSubscribedToBinding(binding string) (bool, error) {
	// if gRPC, looks for the binding in the list of bindings returned from the app
	if !a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
		if a.subscribeBindingList == nil {
			list, err := a.getSubscribedBindingsGRPC()
			if err != nil {
				return false, err
			}
			a.subscribeBindingList = list
		}
		for _, b := range a.subscribeBindingList {
			if b == binding {
				return true, nil
			}
		}
	} else {
		// if HTTP, check if there's an endpoint listening for that binding
		path, _ := a.compStore.GetInputBindingRoute(binding)
		req := invokev1.NewInvokeMethodRequest(path).
			WithHTTPExtension(nethttp.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer req.Close()

		// TODO: Propagate Context
		resp, err := a.channels.AppChannel().InvokeMethod(context.TODO(), req, "")
		if err != nil {
			log.Fatalf("could not invoke OPTIONS method on input binding subscription endpoint %q: %v", path, err)
		}
		defer resp.Close()
		code := resp.Status().Code

		return code/100 == 2 || code == nethttp.StatusMethodNotAllowed, nil
	}
	return false, nil
}

func isBindingOfExplicitDirection(direction string, metadata map[string]string) bool {
	for k, v := range metadata {
		if strings.EqualFold(k, binding.ComponentDirection) {
			directions := strings.Split(v, ",")
			for _, d := range directions {
				if strings.TrimSpace(strings.ToLower(d)) == direction {
					return true
				}
			}
		}
	}

	return false
}

func (a *DaprRuntime) initNameResolution() error {
	var resolver nr.Resolver
	var err error
	resolverMetadata := nr.Metadata{}

	resolverName := a.globalConfig.Spec.NameResolutionSpec.Component
	resolverVersion := a.globalConfig.Spec.NameResolutionSpec.Version

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
	resolverMetadata.Configuration = a.globalConfig.Spec.NameResolutionSpec.Configuration
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
		HealthHTTPClient:   a.appHTTPClient,
		HealthEndpoint:     a.channels.AppHTTPEndpoint(),
		AppChannelAddress:  a.runtimeConfig.appConnectionConfig.ChannelAddress,
	})

	act := actors.NewActors(actors.ActorsOpts{
		AppChannel:       a.channels.AppChannel(),
		GRPCConnectionFn: a.grpc.GetGRPCConnection,
		Config:           actorConfig,
		CertChain:        a.runtimeConfig.certChain,
		TracingSpec:      a.globalConfig.Spec.TracingSpec,
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

func (a *DaprRuntime) processComponents() {
	for comp := range a.pendingComponents {
		if comp.Name == "" {
			continue
		}

		err := a.processComponentAndDependents(comp)
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

func (a *DaprRuntime) processHTTPEndpoints() {
	for endpoint := range a.pendingHTTPEndpoints {
		if endpoint.Name == "" {
			continue
		}
		_, _ = a.processResourceSecrets(&endpoint)
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

func (a *DaprRuntime) processComponentAndDependents(comp componentsV1alpha1.Component) error {
	log.Debug("Loading component: " + comp.LogName())
	res := a.preprocessOneComponent(&comp)
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
		ch <- a.processor.Init(context.TODO(), comp)
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
	a.compStore.AddComponent(comp)
	diag.DefaultMonitoring.ComponentLoaded()

	dependency := componentDependency(compCategory, comp.Name)
	if deps, ok := a.pendingComponentDependents[dependency]; ok {
		delete(a.pendingComponentDependents, dependency)
		for _, dependent := range deps {
			if err := a.processComponentAndDependents(dependent); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *DaprRuntime) preprocessOneComponent(comp *componentsV1alpha1.Component) componentPreprocessRes {
	_, unreadySecretsStore := a.processResourceSecrets(comp)
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

func (a *DaprRuntime) stopWorkflow() {
	if a.workflowEngine != nil && a.workflowEngine.IsRunning {
		log.Info("Shutting down workflow engine")
		a.workflowEngine.Stop(context.TODO())
	}
}

// shutdownOutputComponents allows for a graceful shutdown of all runtime internal operations of components that are not source of more work.
// These are all components except input bindings and pubsub.
func (a *DaprRuntime) shutdownOutputComponents() error {
	log.Info("Shutting down all remaining components")
	var merr error

	// Close components if they implement `io.Closer`
	for name, component := range a.compStore.ListSecretStores() {
		a.compStore.DeleteSecretStore(name)
		merr = errors.Join(merr, closeComponent(component, "secret store "+name))
	}
	for name, component := range a.compStore.ListStateStores() {
		a.compStore.DeleteStateStore(name)
		merr = errors.Join(merr, closeComponent(component, "state store "+name))
	}
	for name, component := range a.compStore.ListLocks() {
		a.compStore.DeleteLock(name)
		merr = errors.Join(merr, closeComponent(component, "clock store "+name))
	}
	for name, component := range a.compStore.ListConfigurations() {
		a.compStore.DeleteConfiguration(name)
		merr = errors.Join(merr, closeComponent(component, "configuration store "+name))
	}
	for name, component := range a.compStore.ListCryptoProviders() {
		a.compStore.DeleteCryptoProvider(name)
		merr = errors.Join(merr, closeComponent(component, "crypto provider "+name))
	}
	for name, component := range a.compStore.ListWorkflows() {
		a.compStore.DeleteWorkflow(name)
		merr = errors.Join(merr, closeComponent(component, "workflow component "+name))
	}
	// Close output bindings
	// Input bindings are closed when a.ctx is canceled
	for name, component := range a.compStore.ListOutputBindings() {
		a.compStore.DeleteOutputBinding(name)
		merr = errors.Join(merr, closeComponent(component, "output binding "+name))
	}
	// Close pubsub publisher
	// The subscriber part is closed when a.ctx is canceled
	for name, pubSub := range a.compStore.ListPubSubs() {
		a.compStore.DeletePubSub(name)
		if pubSub.Component == nil {
			continue
		}
		merr = errors.Join(merr, closeComponent(pubSub.Component, "pubsub "+name))
	}
	merr = errors.Join(merr, closeComponent(a.nameResolver, "name resolver"))

	for _, component := range a.compStore.ListComponents() {
		a.compStore.DeleteComponent(component.Spec.Type, component.Name)
	}

	return merr
}

func closeComponent(component any, logmsg string) error {
	if closer, ok := component.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			err = fmt.Errorf("error closing %s: %w", logmsg, err)
			log.Warn(err)
			return err
		}
	}

	return nil
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

	// Ensure the Unix socket file is removed if a panic occurs.
	defer a.cleanSocket()
	log.Info("Dapr shutting down")
	log.Info("Stopping PubSub subscribers and input bindings")
	a.processor.PubSub().StopSubscriptions()
	a.stopReadingFromBindings()

	// shutdown workflow if it's running
	a.stopWorkflow()

	log.Info("Initiating actor shutdown")
	a.stopActor()

	if a.appHealth != nil {
		log.Info("Closing App Health")
		a.appHealth.Close()
	}

	log.Infof("Holding shutdown for %s to allow graceful stop of outstanding operations", duration.String())
	<-time.After(duration)

	log.Info("Stopping Dapr APIs")
	for _, closer := range a.apiClosers {
		if err := closer.Close(); err != nil {
			log.Warnf("Error closing API: %v", err)
		}
	}
	a.stopTrace()
	a.shutdownOutputComponents()
	// TODO: @joshvanl
	//a.cancel()
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
func (a *DaprRuntime) processResourceSecrets(resource meta.Resource) (updated bool, secretStoreName string) {
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
			// TODO: cascade context.
			r, err := secretStore.GetSecret(context.TODO(), secretstores.GetSecretRequest{
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

func (a *DaprRuntime) blockUntilAppIsReady() {
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
			// TODO: @joshvanl
		//case <-a.ctx.Done():
		//	// Return
		//	return
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

func (a *DaprRuntime) startReadingFromBindings() (err error) {
	if a.channels.AppChannel() == nil {
		return errors.New("app channel not initialized")
	}

	// Clean any previous state
	if a.inputBindingsCancel != nil {
		a.inputBindingsCancel()
	}

	// Input bindings are stopped via cancellation of the main runtime's context
	//a.inputBindingsCtx, a.inputBindingsCancel = context.WithCancel(a.ctx)
	// TODO: @joshvanl
	a.inputBindingsCtx, a.inputBindingsCancel = context.WithCancel(context.TODO())

	for name, bind := range a.compStore.ListInputBindings() {
		var isSubscribed bool
		m := bind.GetComponentMetadata()

		if isBindingOfExplicitDirection(binding.ComponentTypeInput, m) {
			isSubscribed = true
		} else {
			isSubscribed, err = a.isAppSubscribedToBinding(name)
			if err != nil {
				return err
			}
		}

		if !isSubscribed {
			log.Infof("app has not subscribed to binding %s.", name)
			continue
		}

		err = a.readFromBinding(a.inputBindingsCtx, name, bind)
		if err != nil {
			log.Errorf("error reading from input binding %s: %s", name, err)
			continue
		}
	}
	return nil
}

func (a *DaprRuntime) stopReadingFromBindings() {
	if a.inputBindingsCancel != nil {
		a.inputBindingsCancel()
	}

	a.inputBindingsCtx = nil
	a.inputBindingsCancel = nil
}

func createGRPCManager(runtimeConfig *internalConfig, globalConfig *config.Configuration) *grpc.Manager {
	grpcAppChannelConfig := &grpc.AppChannelConfig{}
	if globalConfig != nil {
		grpcAppChannelConfig.TracingSpec = globalConfig.Spec.TracingSpec
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

func (a *DaprRuntime) stopTrace() {
	if a.tracerProvider == nil {
		return
	}
	// Flush and shutdown the tracing provider.
	//shutdownCtx, shutdownCancel := context.WithCancel(a.ctx)
	// TODO: @joshvanl
	shutdownCtx, shutdownCancel := context.WithCancel(context.TODO())
	defer shutdownCancel()
	if err := a.tracerProvider.ForceFlush(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warnf("error flushing tracing provider: %v", err)
	}

	if err := a.tracerProvider.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
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
