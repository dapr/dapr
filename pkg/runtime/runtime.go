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
	"crypto/x509"
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
	"golang.org/x/net/http2"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	md "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/pkg/actors"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	httpChannel "github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/httpendpoint"
	"github.com/dapr/dapr/pkg/internal/apis"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
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
	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/middleware"
	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
)

const (

	// output bindings concurrency.
	bindingsConcurrencyParallel   = "parallel"
	bindingsConcurrencySequential = "sequential"
	pubsubName                    = "pubsubName"

	// hot reloading is currently unsupported, but
	// setting this environment variable restores the
	// partial hot reloading support for k8s.
	hotReloadingEnvVar = "DAPR_ENABLE_HOT_RELOADING"

	defaultComponentInitTimeout = time.Second * 5
)

var log = logger.NewLogger("dapr.runtime")

// ErrUnexpectedEnvelopeData denotes that an unexpected data type
// was encountered when processing a cloud event's data property.
var ErrUnexpectedEnvelopeData = errors.New("unexpected data type encountered in envelope")

var cloudEventDuplicateKeys = sets.NewString(pubsub.IDField, pubsub.SourceField, pubsub.DataContentTypeField, pubsub.TypeField, pubsub.SpecVersionField, pubsub.DataField, pubsub.DataBase64Field)

// Type of function that determines if a component is authorized.
// The function receives the component and must return true if the component is authorized.
type ComponentAuthorizer func(component componentsV1alpha1.Component) bool

// Type of function that determines if an http endpoint is authorized.
// The function receives the http endpoint and must return true if the http endpoint is authorized.
type HTTPEndpointAuthorizer func(endpoint httpEndpointV1alpha1.HTTPEndpoint) bool

// DaprRuntime holds all the core components of the runtime.
type DaprRuntime struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	runtimeConfig           *internalConfig
	globalConfig            *config.Configuration
	accessControlList       *config.AccessControlList
	grpc                    *grpc.Manager
	appChannel              channel.AppChannel             // 1:1 relationship between sidecar and app for communication.
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
	pubsubCtx               context.Context
	pubsubCancel            context.CancelFunc
	topicsLock              *sync.RWMutex
	topicCtxCancels         map[string]context.CancelFunc // Key is "componentName||topicName"
	shutdownC               chan error
	running                 atomic.Bool
	apiClosers              []io.Closer
	componentAuthorizers    []ComponentAuthorizer
	httpEndpointAuthorizers []HTTPEndpointAuthorizer
	appHealth               *apphealth.AppHealth
	appHealthReady          func() // Invoked the first time the app health becomes ready
	appHealthLock           *sync.Mutex
	bulkSubLock             *sync.Mutex
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
func newDaprRuntime(runtimeConfig *internalConfig, globalConfig *config.Configuration, accessControlList *config.AccessControlList, resiliencyProvider resiliency.Provider) *DaprRuntime {
	ctx, cancel := context.WithCancel(context.Background())

	wfe := wfengine.NewWorkflowEngine(wfengine.NewWorkflowConfig(runtimeConfig.id))
	wfe.ConfigureGrpcExecutor()

	meta := meta.New(meta.Options{
		ID:        runtimeConfig.id,
		PodName:   getPodName(),
		Namespace: getNamespace(),
		Mode:      runtimeConfig.mode,
	})
	compStore := compstore.New()
	rt := &DaprRuntime{
		ctx:                        ctx,
		cancel:                     cancel,
		runtimeConfig:              runtimeConfig,
		globalConfig:               globalConfig,
		accessControlList:          accessControlList,
		grpc:                       createGRPCManager(runtimeConfig, globalConfig),
		topicsLock:                 &sync.RWMutex{},
		pendingHTTPEndpoints:       make(chan httpEndpointV1alpha1.HTTPEndpoint),
		pendingComponents:          make(chan componentsV1alpha1.Component),
		pendingComponentDependents: map[string][]componentsV1alpha1.Component{},
		shutdownC:                  make(chan error, 1),
		tracerProvider:             nil,
		resiliency:                 resiliencyProvider,
		workflowEngine:             wfe,
		appHealthReady:             nil,
		appHealthLock:              &sync.Mutex{},
		bulkSubLock:                &sync.Mutex{},
		compStore:                  compStore,
		meta:                       meta,
		processor: processor.New(processor.Options{
			ID:               runtimeConfig.id,
			PlacementEnabled: len(runtimeConfig.placementAddresses) > 0,
			Registry:         runtimeConfig.registry,
			ComponentStore:   compStore,
			Meta:             meta,
		}),
	}

	rt.componentAuthorizers = []ComponentAuthorizer{rt.namespaceComponentAuthorizer}
	if globalConfig != nil && globalConfig.Spec.ComponentsSpec != nil && len(globalConfig.Spec.ComponentsSpec.Deny) > 0 {
		dl := newComponentDenyList(globalConfig.Spec.ComponentsSpec.Deny)
		rt.componentAuthorizers = append(rt.componentAuthorizers, dl.IsAllowed)
	}

	rt.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{rt.namespaceHTTPEndpointAuthorizer}

	rt.initAppHTTPClient()

	return rt
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

func (a *DaprRuntime) getOperatorClient() (operatorv1pb.OperatorClient, error) {
	// Get the operator client only if we're running in Kubernetes and if we need it
	if a.runtimeConfig.mode != modes.KubernetesMode {
		return nil, nil
	}

	client, _, err := client.GetOperatorClient(context.TODO(), a.runtimeConfig.kubernetes.ControlPlaneAddress, security.TLSServerName, a.runtimeConfig.certChain)
	if err != nil {
		return nil, fmt.Errorf("error creating operator client: %w", err)
	}
	return client, nil
}

// setupTracing set up the trace exporters. Technically we don't need to pass `hostAddress` in,
// but we do so here to explicitly call out the dependency on having `hostAddress` computed.
func (a *DaprRuntime) setupTracing(hostAddress string, tpStore tracerProviderStore) error {
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
		otelExporter, err := otlptrace.New(a.ctx, client)
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
	a.operatorClient, err = a.getOperatorClient()
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

	pipeline, err := a.buildHTTPPipeline()
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

	err = a.createChannels()
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.appConnectionConfig.Protocol), err)
	}

	endpointChannels, err := a.createHTTPEndpointsChannels()
	if err != nil {
		log.Warnf("failed to open channels for http endpoints: %s", err)
	}

	a.daprHTTPAPI.SetAppChannel(a.appChannel)
	a.daprGRPCAPI.SetAppChannel(a.appChannel)
	a.directMessaging.SetAppChannel(a.appChannel)

	a.directMessaging.SetHTTPEndpointsAppChannels(a.httpEndpointsAppChannel, endpointChannels)

	a.daprHTTPAPI.SetDirectMessaging(a.directMessaging)
	a.daprGRPCAPI.SetDirectMessaging(a.directMessaging)

	if a.runtimeConfig.appConnectionConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.appConnectionConfig.MaxConcurrency)
	}

	a.appHealthReady = func() {
		a.appHealthReadyInit(ctx)
	}
	if a.runtimeConfig.appConnectionConfig.HealthCheck != nil && a.appChannel != nil {
		// We can't just pass "a.appChannel.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.New(*a.runtimeConfig.appConnectionConfig.HealthCheck, func(ctx context.Context) (bool, error) {
			return a.appChannel.HealthProbe(ctx)
		})
		a.appHealth.OnHealthChange(a.appHealthChanged)
		a.appHealth.StartProbes(a.ctx)

		// Set the appHealth object in the channel so it's aware of the app's health status
		a.appChannel.SetAppHealth(a.appHealth)

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
			if componentInitErr := a.processor.One(ctx, wfengine.ComponentDefinition); componentInitErr != nil {
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
	if err := pluggable.Discover(a.ctx); err != nil {
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
		a.startSubscriptions()
		err := a.startReadingFromBindings()
		if err != nil {
			log.Warnf("failed to read from bindings: %s ", err)
		}
	case apphealth.AppStatusUnhealthy:
		// Stop topic subscriptions and input bindings
		a.stopSubscriptions()
		a.stopReadingFromBindings()
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

func (a *DaprRuntime) buildHTTPPipelineForSpec(spec config.PipelineSpec, targetPipeline string) (pipeline httpMiddleware.Pipeline, err error) {
	pipeline.Handlers = make([]func(next nethttp.Handler) nethttp.Handler, 0, len(spec.Handlers))
	for i := 0; i < len(spec.Handlers); i++ {
		middlewareSpec := spec.Handlers[i]
		component, exists := a.compStore.GetComponent(middlewareSpec.Type, middlewareSpec.Name)
		if !exists {
			// Log the error but continue with initializing the pipeline
			log.Error("couldn't find middleware component defined in configuration with name %s", middlewareSpec.LogName())
			continue
		}
		md := middleware.Metadata{Base: a.meta.ToBaseMetadata(component)}
		handler, err := a.runtimeConfig.registry.HTTPMiddlewares().Create(middlewareSpec.Type, middlewareSpec.Version, md, middlewareSpec.LogName())
		if err != nil {
			e := fmt.Sprintf("process component %s error: %s", component.Name, err.Error())
			if !component.Spec.IgnoreErrors {
				log.Warn("error processing middleware component, daprd process will exit gracefully")
				a.Shutdown(a.runtimeConfig.gracefulShutdownDuration)
				log.Fatal(e)
				// This error is only caught by tests, since during normal execution we panic
				return pipeline, errors.New("dapr panicked")
			}
			log.Error(e)
			continue
		}
		log.Infof("enabled %s/%s %s middleware", middlewareSpec.Type, targetPipeline, middlewareSpec.Version)
		pipeline.Handlers = append(pipeline.Handlers, handler)
	}

	return pipeline, nil
}

func (a *DaprRuntime) buildHTTPPipeline() (httpMiddleware.Pipeline, error) {
	if a.globalConfig == nil || a.globalConfig.Spec.HTTPPipelineSpec == nil {
		return httpMiddleware.Pipeline{}, nil
	}
	return a.buildHTTPPipelineForSpec(*a.globalConfig.Spec.HTTPPipelineSpec, "http")
}

func (a *DaprRuntime) buildAppHTTPPipeline() (httpMiddleware.Pipeline, error) {
	if a.globalConfig == nil || a.globalConfig.Spec.AppHTTPPipelineSpec == nil {
		return httpMiddleware.Pipeline{}, nil
	}
	return a.buildHTTPPipelineForSpec(*a.globalConfig.Spec.AppHTTPPipelineSpec, "app channel")
}

func (a *DaprRuntime) sendToDeadLetter(name string, msg *pubsub.NewMessage, deadLetterTopic string) (err error) {
	req := &pubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	err = a.Publish(req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}
	return nil
}

func (a *DaprRuntime) subscribeTopic(parentCtx context.Context, name string, topic string, route compstore.TopicRouteElem) error {
	subKey := pubsubTopicKey(name, topic)

	a.topicsLock.Lock()
	defer a.topicsLock.Unlock()

	pubSub, ok := a.compStore.GetPubSub(name)
	if !ok {
		return fmt.Errorf("pubsub '%s' not found", name)
	}

	allowed := a.isPubSubOperationAllowed(name, topic, pubSub.ScopedSubscriptions)
	if !allowed {
		return fmt.Errorf("subscription to topic '%s' on pubsub '%s' is not allowed", topic, name)
	}

	log.Debugf("subscribing to topic='%s' on pubsub='%s'", topic, name)

	if _, ok := a.topicCtxCancels[subKey]; ok {
		return fmt.Errorf("cannot subscribe to topic '%s' on pubsub '%s': the subscription already exists", topic, name)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	policyDef := a.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.Metadata

	namespaced := pubSub.NamespaceScoped

	if route.BulkSubscribe != nil && route.BulkSubscribe.Enabled {
		err := a.bulkSubscribeTopic(ctx, policyDef, name, topic, route, namespaced)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to bulk subscribe to topic %s: %w", topic, err)
		}
		a.topicCtxCancels[subKey] = cancel
		return nil
	}

	subscribeTopic := topic
	if namespaced {
		subscribeTopic = a.namespace + topic
	}

	err := pubSub.Component.Subscribe(ctx, pubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *pubsub.NewMessage) error {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[pubsubName] = name

		msgTopic := msg.Topic
		if pubSub.NamespaceScoped {
			msgTopic = strings.Replace(msgTopic, a.namespace, "", 1)
		}

		rawPayload, err := contribMetadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if route.DeadLetterTopic != "" {
				if dlqErr := a.sendToDeadLetter(name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msgTopic, 0)
			return err
		}

		var cloudEvent map[string]interface{}
		data := msg.Data
		if rawPayload {
			cloudEvent = pubsub.FromRawPayload(msg.Data, msgTopic, name)
			data, err = json.Marshal(cloudEvent)
			if err != nil {
				log.Errorf("error serializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := a.sendToDeadLetter(name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msgTopic, 0)
				return err
			}
		} else {
			err = json.Unmarshal(msg.Data, &cloudEvent)
			if err != nil {
				log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := a.sendToDeadLetter(name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msgTopic, 0)
				return err
			}
		}

		if pubsub.HasExpired(cloudEvent) {
			log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.ExpirationField])
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)

			if route.DeadLetterTopic != "" {
				_ = a.sendToDeadLetter(name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		routePath, shouldProcess, err := findMatchingRoute(route.Rules, cloudEvent)
		if err != nil {
			log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[pubsub.IDField], name, msgTopic, err)
			if route.DeadLetterTopic != "" {
				if dlqErr := a.sendToDeadLetter(name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msgTopic, 0)
			return err
		}
		if !shouldProcess {
			// The event does not match any route specified so ignore it.
			log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[pubsub.IDField], name, msgTopic)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msgTopic, 0)
			if route.DeadLetterTopic != "" {
				_ = a.sendToDeadLetter(name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		psm := &pubsubSubscribedMessage{
			cloudEvent: cloudEvent,
			data:       data,
			topic:      msgTopic,
			metadata:   msg.Metadata,
			path:       routePath,
			pubsub:     name,
		}
		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			var pErr error
			if a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
				pErr = a.publishMessageHTTP(ctx, psm)
			} else {
				pErr = a.publishMessageGRPC(ctx, psm)
			}
			var rErr *rterrors.RetriableError
			if errors.As(pErr, &rErr) {
				log.Warnf("encountered a retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, rErr.Unwrap())
			} else if pErr != nil {
				log.Errorf("encountered a non-retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, pErr)
			}
			return nil, pErr
		})
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.DeadLetterTopic == "" {
				return err
			}
			_ = a.sendToDeadLetter(name, msg, route.DeadLetterTopic)
			return nil
		}
		return err
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}
	a.topicCtxCancels[subKey] = cancel
	return nil
}

func (a *DaprRuntime) unsubscribeTopic(name string, topic string) error {
	a.topicsLock.Lock()
	defer a.topicsLock.Unlock()

	subKey := pubsubTopicKey(name, topic)
	cancel, ok := a.topicCtxCancels[subKey]
	if !ok {
		return fmt.Errorf("cannot unsubscribe from topic '%s' on pubsub '%s': the subscription does not exist", topic, name)
	}

	if cancel != nil {
		cancel()
	}

	delete(a.topicCtxCancels, subKey)

	return nil
}

func (a *DaprRuntime) beginPubSub(name string) error {
	topicRoutes, err := a.getTopicRoutes()
	if err != nil {
		return err
	}

	v, ok := topicRoutes[name]
	if !ok {
		return nil
	}

	for topic, route := range v {
		err = a.subscribeTopic(a.pubsubCtx, name, topic, route)
		if err != nil {
			// Log the error only
			log.Errorf("error occurred while beginning pubsub for topic %s on component %s: %v", topic, name, err)
		}
	}

	return nil
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*runtimePubsub.Rule, cloudEvent interface{}) (path string, shouldProcess bool, err error) {
	hasRules := len(rules) > 0
	if hasRules {
		data := map[string]interface{}{
			"event": cloudEvent,
		}
		rule, err := matchRoutingRule(rules, data)
		if err != nil {
			return "", false, err
		}
		if rule != nil {
			return rule.Path, true, nil
		}
	}

	return "", false, nil
}

func matchRoutingRule(rules []*runtimePubsub.Rule, data map[string]interface{}) (*runtimePubsub.Rule, error) {
	for _, rule := range rules {
		if rule.Match == nil {
			return rule, nil
		}
		iResult, err := rule.Match.Eval(data)
		if err != nil {
			return nil, err
		}
		result, ok := iResult.(bool)
		if !ok {
			return nil, fmt.Errorf("the result of match expression %s was not a boolean", rule.Match)
		}

		if result {
			return rule, nil
		}
	}

	return nil, nil
}

func (a *DaprRuntime) initDirectMessaging(resolver nr.Resolver) {
	a.directMessaging = messaging.NewDirectMessaging(messaging.NewDirectMessagingOpts{
		AppID:              a.runtimeConfig.id,
		Namespace:          a.namespace,
		Port:               a.runtimeConfig.internalGRPCPort,
		Mode:               a.runtimeConfig.mode,
		AppChannel:         a.appChannel,
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
		for a.ctx.Err() == nil {
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
	a.processHTTPEndpointSecrets(&endpoint)

	if exists && reflect.DeepEqual(oldEndpoint.Spec, endpoint.Spec) {
		return false
	}

	a.pendingHTTPEndpoints <- endpoint
	return true
}

func (a *DaprRuntime) processHTTPEndpointSecrets(endpoint *httpEndpointV1alpha1.HTTPEndpoint) {
	_, _ = a.processResourceSecrets(endpoint)

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

	updated, _ := a.processResourceSecrets(&tlsResource)
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
				policyRunner := resiliency.NewRunner[*bindings.InvokeResponse](a.ctx,
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

			err := stateLoader.PerformBulkStoreOperation(a.ctx, reqs,
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

		if response.Concurrency == bindingsConcurrencyParallel {
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
	ctx, span := diag.StartInternalCallbackSpan(a.ctx, spanName, spanContext, a.globalConfig.Spec.TracingSpec)

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
				response.Concurrency = bindingsConcurrencyParallel
			} else {
				response.Concurrency = bindingsConcurrencySequential
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
			rResp, rErr := a.appChannel.InvokeMethod(ctx, req, "")
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
		AppChannel:                  a.appChannel,
		HTTPEndpointsAppChannel:     a.httpEndpointsAppChannel,
		DirectMessaging:             a.directMessaging,
		Resiliency:                  a.resiliency,
		PubsubAdapter:               a.getPublishAdapter(),
		Actors:                      a.actor,
		SendToOutputBindingFn:       a.sendToOutputBinding,
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
		AppChannel:                  a.appChannel,
		Resiliency:                  a.resiliency,
		PubsubAdapter:               a.getPublishAdapter(),
		DirectMessaging:             a.directMessaging,
		Actors:                      a.actor,
		SendToOutputBindingFn:       a.sendToOutputBinding,
		TracingSpec:                 a.globalConfig.GetTracingSpec(),
		AccessControlList:           a.accessControlList,
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		CompStore:                   a.compStore,
		AppConnectionConfig:         a.runtimeConfig.appConnectionConfig,
		GlobalConfig:                a.globalConfig,
	})
}

func (a *DaprRuntime) getPublishAdapter() runtimePubsub.Adapter {
	if a.compStore.PubSubsLen() == 0 {
		return nil
	}

	return a
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
		resp, err := a.appChannel.InvokeMethod(context.TODO(), req, "")
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
		if strings.EqualFold(k, processor.BindingDirection) {
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

// Refer for state store api decision  https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (a *DaprRuntime) getDeclarativeSubscriptions() []runtimePubsub.Subscription {
	var subs []runtimePubsub.Subscription

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		subs = runtimePubsub.DeclarativeKubernetes(a.operatorClient, a.podName, a.namespace, log)
	case modes.StandaloneMode:
		subs = runtimePubsub.DeclarativeLocal(a.runtimeConfig.standalone.ResourcesPath, a.namespace, log)
	}

	// only return valid subscriptions for this app id
	i := 0
	for _, s := range subs {
		keep := false
		if len(s.Scopes) == 0 {
			keep = true
		} else {
			for _, scope := range s.Scopes {
				if scope == a.runtimeConfig.id {
					keep = true
					break
				}
			}
		}

		if keep {
			subs[i] = s
			i++
		}
	}
	return subs[:i]
}

func (a *DaprRuntime) getSubscriptions() ([]runtimePubsub.Subscription, error) {
	if subs := a.compStore.ListSubscriptions(); len(subs) > 0 {
		return subs, nil
	}

	var (
		subscriptions []runtimePubsub.Subscription
		err           error
	)

	if a.appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return subscriptions, nil
	}

	// handle app subscriptions
	if a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
		subscriptions, err = runtimePubsub.GetSubscriptionsHTTP(a.appChannel, log, a.resiliency)
	} else {
		var conn gogrpc.ClientConnInterface
		conn, err = a.grpc.GetAppClient()
		defer a.grpc.ReleaseAppClient(conn)
		if err != nil {
			return nil, fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		subscriptions, err = runtimePubsub.GetSubscriptionsGRPC(client, log, a.resiliency)
	}
	if err != nil {
		return nil, err
	}

	// handle declarative subscriptions
	ds := a.getDeclarativeSubscriptions()
	for _, s := range ds {
		skip := false

		// don't register duplicate subscriptions
		for _, sub := range subscriptions {
			if sub.PubsubName == s.PubsubName && sub.Topic == s.Topic {
				log.Warnf("two identical subscriptions found (sources: declarative, app endpoint). pubsubname: %s, topic: %s",
					s.PubsubName, s.Topic)
				skip = true
				break
			}
		}

		if !skip {
			subscriptions = append(subscriptions, s)
		}
	}

	a.compStore.SetSubscriptions(subscriptions)
	return subscriptions, nil
}

func (a *DaprRuntime) getTopicRoutes() (map[string]compstore.TopicRoutes, error) {
	if !a.compStore.TopicRoutesIsNil() {
		return a.compStore.GetTopicRoutes(), nil
	}

	topicRoutes := make(map[string]compstore.TopicRoutes)

	if a.appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return topicRoutes, nil
	}

	subscriptions, err := a.getSubscriptions()
	if err != nil {
		return nil, err
	}

	for _, s := range subscriptions {
		if topicRoutes[s.PubsubName] == nil {
			topicRoutes[s.PubsubName] = compstore.TopicRoutes{}
		}

		topicRoutes[s.PubsubName][s.Topic] = compstore.TopicRouteElem{
			Metadata:        s.Metadata,
			Rules:           s.Rules,
			DeadLetterTopic: s.DeadLetterTopic,
			BulkSubscribe:   s.BulkSubscribe,
		}
	}

	if len(topicRoutes) > 0 {
		for pubsubName, v := range topicRoutes {
			var topics string
			for topic := range v {
				if topics == "" {
					topics += topic
				} else {
					topics += " " + topic
				}
			}
			log.Infof("app is subscribed to the following topics: [%s] through pubsub=%s", topics, pubsubName)
		}
	}
	a.compStore.SetTopicRoutes(topicRoutes)
	return topicRoutes, nil
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (a *DaprRuntime) Publish(req *pubsub.PublishRequest) error {
	a.topicsLock.RLock()
	ps, ok := a.compStore.GetPubSub(req.PubsubName)
	a.topicsLock.RUnlock()

	if !ok {
		return runtimePubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, ps.ScopedPublishings); !allowed {
		return runtimePubsub.NotAllowedError{Topic: req.Topic, ID: a.runtimeConfig.id}
	}

	if ps.NamespaceScoped {
		req.Topic = a.namespace + req.Topic
	}

	policyRunner := resiliency.NewRunner[any](a.ctx,
		a.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, ps.Component.Publish(ctx, req)
	})
	return err
}

func (a *DaprRuntime) BulkPublish(req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	// context.TODO() is used here as later on a context will have to be passed in for each publish separately
	ps, ok := a.compStore.GetPubSub(req.PubsubName)
	if !ok {
		return pubsub.BulkPublishResponse{}, runtimePubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, ps.ScopedPublishings); !allowed {
		return pubsub.BulkPublishResponse{}, runtimePubsub.NotAllowedError{Topic: req.Topic, ID: a.runtimeConfig.id}
	}

	policyDef := a.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub)

	if pubsub.FeatureBulkPublish.IsPresent(ps.Component.Features()) {
		return runtimePubsub.ApplyBulkPublishResiliency(context.TODO(), req, policyDef, ps.Component.(pubsub.BulkPublisher))
	}

	log.Debugf("pubsub %s does not implement the BulkPublish API; falling back to publishing messages individually", req.PubsubName)
	defaultBulkPublisher := runtimePubsub.NewDefaultBulkPublisher(ps.Component)

	return runtimePubsub.ApplyBulkPublishResiliency(context.TODO(), req, policyDef, defaultBulkPublisher)
}

// Subscribe is used by APIs to start a subscription to a topic.
func (a *DaprRuntime) Subscribe(ctx context.Context, name string, routes map[string]compstore.TopicRouteElem) (err error) {
	_, ok := a.compStore.GetPubSub(name)
	if !ok {
		return fmt.Errorf("pubsub component %s does not exist", name)
	}

	for topic, route := range routes {
		err = a.subscribeTopic(ctx, name, topic, route)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetPubSub is an adapter method to find a pubsub by name.
func (a *DaprRuntime) GetPubSub(pubsubName string) pubsub.PubSub {
	ps, ok := a.compStore.GetPubSub(pubsubName)
	if !ok {
		return nil
	}
	return ps.Component
}

func (a *DaprRuntime) isPubSubOperationAllowed(pubsubName string, topic string, scopedTopics []string) bool {
	inAllowedTopics := false

	pubSub, ok := a.compStore.GetPubSub(pubsubName)
	if !ok {
		return false
	}

	// first check if allowedTopics contain it
	if len(pubSub.AllowedTopics) > 0 {
		for _, t := range pubSub.AllowedTopics {
			if t == topic {
				inAllowedTopics = true
				break
			}
		}
		if !inAllowedTopics {
			return false
		}
	}
	if len(scopedTopics) == 0 {
		return true
	}

	// check if a granular scope has been applied
	allowedScope := false
	for _, t := range scopedTopics {
		if t == topic {
			allowedScope = true
			break
		}
	}
	return allowedScope
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

func (a *DaprRuntime) publishMessageHTTP(ctx context.Context, msg *pubsubSubscribedMessage) error {
	cloudEvent := msg.cloudEvent

	var span trace.Span

	req := invokev1.NewInvokeMethodRequest(msg.path).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(msg.data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(msg.metadata)
	defer req.Close()

	iTraceID := cloudEvent[pubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[pubsub.TraceIDField]
	}
	if iTraceID != nil {
		traceID := iTraceID.(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.topic, sc, a.globalConfig.Spec.TracingSpec)
	}

	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req, "")
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(err))
	}
	defer resp.Close()

	statusCode := int(resp.Status().Code)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse pubsub.AppResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appResponse)
		// We need to return an error here since the app didn't return a valid
		// AppResponse.
		if errors.Is(err, io.EOF) {
			return err
		}
		if err != nil {
			log.Debugf("skipping status check due to error parsing result from pub/sub event %v: %s", cloudEvent[pubsub.IDField], err)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
			return nil
		}

		switch appResponse.Status {
		case "":
			// Consider empty status field as success
			fallthrough
		case pubsub.Success:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
			return nil
		case pubsub.Retry:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
			return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[pubsub.IDField], rterrors.NewRetriable(nil))
		case pubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
			return nil
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[pubsub.IDField], appResponse.Status, rterrors.NewRetriable(nil))
	}

	body, _ := resp.RawDataFull()
	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent[pubsub.IDField], body, statusCode)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
		return nil
	}

	// Every error from now on is a retriable error.
	errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.TopicField], body, statusCode)
	log.Warnf(errMsg)
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	return rterrors.NewRetriable(errors.New(errMsg))
}

func (a *DaprRuntime) publishMessageGRPC(ctx context.Context, msg *pubsubSubscribedMessage) error {
	cloudEvent := msg.cloudEvent

	envelope := &runtimev1pb.TopicEventRequest{
		Id:              extractCloudEventProperty(cloudEvent, pubsub.IDField),
		Source:          extractCloudEventProperty(cloudEvent, pubsub.SourceField),
		DataContentType: extractCloudEventProperty(cloudEvent, pubsub.DataContentTypeField),
		Type:            extractCloudEventProperty(cloudEvent, pubsub.TypeField),
		SpecVersion:     extractCloudEventProperty(cloudEvent, pubsub.SpecVersionField),
		Topic:           msg.topic,
		PubsubName:      msg.metadata[pubsubName],
		Path:            msg.path,
	}

	if data, ok := cloudEvent[pubsub.DataBase64Field]; ok && data != nil {
		if dataAsString, ok := data.(string); ok {
			decoded, decodeErr := base64.StdEncoding.DecodeString(dataAsString)
			if decodeErr != nil {
				log.Debugf("unable to base64 decode cloudEvent field data_base64: %s", decodeErr)
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, 0)

				return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(decodeErr))
			}

			envelope.Data = decoded
		} else {
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, 0)
			return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(ErrUnexpectedEnvelopeData))
		}
	} else if data, ok := cloudEvent[pubsub.DataField]; ok && data != nil {
		envelope.Data = nil

		if contenttype.IsStringContentType(envelope.DataContentType) {
			switch v := data.(type) {
			case string:
				envelope.Data = []byte(v)
			case []byte:
				envelope.Data = v
			default:
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, 0)
				return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(ErrUnexpectedEnvelopeData))
			}
		} else if contenttype.IsJSONContentType(envelope.DataContentType) || contenttype.IsCloudEventContentType(envelope.DataContentType) {
			envelope.Data, _ = json.Marshal(data)
		}
	}

	var span trace.Span
	iTraceID := cloudEvent[pubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[pubsub.TraceIDField]
	}
	if iTraceID != nil {
		if traceID, ok := iTraceID.(string); ok {
			sc, _ := diag.SpanContextFromW3CString(traceID)
			spanName := fmt.Sprintf("pubsub/%s", msg.topic)

			// no ops if trace is off
			ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, a.globalConfig.Spec.TracingSpec)
			// span is nil if tracing is disabled (sampling rate is 0)
			if span != nil {
				ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
			}
		} else {
			log.Warnf("ignored non-string traceid value: %v", iTraceID)
		}
	}

	extensions, extensionsErr := extractCloudEventExtensions(cloudEvent)
	if extensionsErr != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, 0)
		return extensionsErr
	}
	envelope.Extensions = extensions

	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.metadata)

	conn, err := a.grpc.GetAppClient()
	defer a.grpc.ReleaseAppClient(conn)
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := runtimev1pb.NewAppCallbackClient(conn)

	start := time.Now()
	res, err := clientV1.OnTopicEvent(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
		span.End()
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing pub/sub event %v: %s", cloudEvent[pubsub.IDField], err)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)

			return nil
		}

		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", cloudEvent[pubsub.IDField], rterrors.NewRetriable(err))
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)

		// on error from application, return error for redelivery of event
		return err
	}

	switch res.GetStatus() {
	case runtimev1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
		return nil
	case runtimev1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[pubsub.IDField], rterrors.NewRetriable(nil))
	case runtimev1pb.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)

		return nil
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[pubsub.IDField], res.GetStatus(), rterrors.NewRetriable(nil))
}

func extractCloudEventExtensions(cloudEvent map[string]interface{}) (*structpb.Struct, error) {
	// Assemble Cloud Event Extensions:
	// Create copy of the cloud event with duplicated data removed

	extensions := map[string]interface{}{}
	for key, value := range cloudEvent {
		if !cloudEventDuplicateKeys.Has(key) {
			extensions[key] = value
		}
	}
	extensionsStruct := structpb.Struct{}
	extensionBytes, jsonMarshalErr := json.Marshal(extensions)
	if jsonMarshalErr != nil {
		return &extensionsStruct, fmt.Errorf("error processing internal cloud event data: unable to marshal cloudEvent extensions: %s", jsonMarshalErr)
	}

	protoUnmarshalErr := protojson.Unmarshal(extensionBytes, &extensionsStruct)
	if protoUnmarshalErr != nil {
		return &extensionsStruct, fmt.Errorf("error processing internal cloud event data: unable to unmarshal cloudEvent extensions to proto struct: %s", protoUnmarshalErr)
	}
	return &extensionsStruct, nil
}

func extractCloudEventProperty(cloudEvent map[string]interface{}, property string) string {
	if cloudEvent == nil {
		return ""
	}
	iValue, ok := cloudEvent[property]
	if ok {
		if value, ok := iValue.(string); ok {
			return value
		}
	}

	return ""
}

func (a *DaprRuntime) initActors() error {
	err := actors.ValidateHostEnvironment(a.runtimeConfig.mTLSEnabled, a.runtimeConfig.mode, a.namespace)
	if err != nil {
		return rterrors.NewInit(rterrors.InitFailure, "actors", err)
	}
	a.actorStateStoreLock.Lock()
	defer a.actorStateStoreLock.Unlock()

	actorStateStoreName, ok := a.processor.ActorStateStore()
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
		HealthEndpoint:     a.getAppHTTPEndpoint(),
		AppChannelAddress:  a.runtimeConfig.appConnectionConfig.ChannelAddress,
		PodName:            getPodName(),
	})

	act := actors.NewActors(actors.ActorsOpts{
		AppChannel:       a.appChannel,
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
		a.processHTTPEndpointSecrets(&endpoint)
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
		ch <- a.processor.One(context.TODO(), comp)
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
	if a.workflowEngine != nil {
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
	a.stopSubscriptions()
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
	a.cancel()
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
		case <-a.ctx.Done():
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
	if a.appChannel == nil {
		return
	}

	appConfig, err := a.appChannel.GetAppConfig(a.runtimeConfig.id)
	if err != nil {
		return
	}

	if appConfig != nil {
		a.appConfig = *appConfig
		log.Info("Application configuration loaded")
	}
}

// Returns the HTTP endpoint for the app.
func (a *DaprRuntime) getAppHTTPEndpoint() string {
	// Application protocol is "http" or "https"
	port := strconv.Itoa(a.runtimeConfig.appConnectionConfig.Port)
	switch a.runtimeConfig.appConnectionConfig.Protocol {
	case protocol.HTTPProtocol, protocol.H2CProtocol:
		return "http://" + a.runtimeConfig.appConnectionConfig.ChannelAddress + ":" + port
	case protocol.HTTPSProtocol:
		return "https://" + a.runtimeConfig.appConnectionConfig.ChannelAddress + ":" + port
	default:
		return ""
	}
}

// Initializes the appHTTPClient property.
func (a *DaprRuntime) initAppHTTPClient() {
	var transport nethttp.RoundTripper
	if a.runtimeConfig.appConnectionConfig.Protocol == protocol.H2CProtocol {
		// Enable HTTP/2 Cleartext transport
		transport = &http2.Transport{
			AllowHTTP: true, // To enable using "http" as protocol
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				// Return the TCP socket without TLS
				return net.Dial(network, addr)
			},
			// TODO: This may not be exactly the same as "MaxResponseHeaderBytes" so check before enabling this
			// MaxHeaderListSize: uint32(a.runtimeConfig.readBufferSize << 10),
		}
	} else {
		var tlsConfig *tls.Config
		if a.runtimeConfig.appConnectionConfig.Protocol == protocol.HTTPSProtocol {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec
				// For 1.11
				MinVersion: channel.AppChannelMinTLSVersion,
			}
			// TODO: Remove when the feature is finalized
			if a.globalConfig.IsFeatureEnabled(config.AppChannelAllowInsecureTLS) {
				tlsConfig.MinVersion = 0
			}
		}

		transport = &nethttp.Transport{
			TLSClientConfig:        tlsConfig,
			ReadBufferSize:         a.runtimeConfig.readBufferSize << 10,
			MaxResponseHeaderBytes: int64(a.runtimeConfig.readBufferSize) << 10,
			MaxConnsPerHost:        1024,
			MaxIdleConns:           64, // A local channel connects to a single host
			MaxIdleConnsPerHost:    64,
		}
	}

	// Initialize this property in the object, and then pass it to the HTTP channel and the actors runtime (for health checks)
	// We want to re-use the same client so TCP sockets can be re-used efficiently across everything that communicates with the app
	// This is especially useful if the app supports HTTP/2
	a.appHTTPClient = &nethttp.Client{
		Transport: transport,
		CheckRedirect: func(req *nethttp.Request, via []*nethttp.Request) error {
			return nethttp.ErrUseLastResponse
		},
	}
}

func (a *DaprRuntime) getAppHTTPChannelConfig(pipeline httpMiddleware.Pipeline) httpChannel.ChannelConfiguration {
	conf := httpChannel.ChannelConfiguration{
		CompStore:            a.compStore,
		MaxConcurrency:       a.runtimeConfig.appConnectionConfig.MaxConcurrency,
		Pipeline:             pipeline,
		TracingSpec:          a.globalConfig.GetTracingSpec(),
		MaxRequestBodySizeMB: a.runtimeConfig.maxRequestBodySize,
	}

	conf.Endpoint = a.getAppHTTPEndpoint()
	conf.Client = a.appHTTPClient
	return conf
}

func (a *DaprRuntime) getHTTPEndpointAppChannel(pipeline httpMiddleware.Pipeline, endpoint httpEndpointV1alpha1.HTTPEndpoint) (httpChannel.ChannelConfiguration, error) {
	conf := httpChannel.ChannelConfiguration{
		CompStore:            a.compStore,
		MaxConcurrency:       a.runtimeConfig.appConnectionConfig.MaxConcurrency,
		Pipeline:             pipeline,
		MaxRequestBodySizeMB: a.runtimeConfig.maxRequestBodySize,
	}

	if a.globalConfig.Spec.TracingSpec != nil {
		conf.TracingSpec = *a.globalConfig.Spec.TracingSpec
	}

	var tlsConfig *tls.Config

	if endpoint.HasTLSRootCA() {
		ca := endpoint.Spec.ClientTLS.RootCA.Value.String()
		caCertPool := x509.NewCertPool()

		if !caCertPool.AppendCertsFromPEM([]byte(ca)) {
			return httpChannel.ChannelConfiguration{}, fmt.Errorf("failed to add root cert to cert pool for http endpoint %s", endpoint.ObjectMeta.Name)
		}

		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    caCertPool,
		}
	}

	if endpoint.HasTLSPrivateKey() {
		cert, err := tls.X509KeyPair([]byte(endpoint.Spec.ClientTLS.Certificate.Value.String()), []byte(endpoint.Spec.ClientTLS.PrivateKey.Value.String()))
		if err != nil {
			return httpChannel.ChannelConfiguration{}, fmt.Errorf("failed to load client certificate for http endpoint %s: %w", endpoint.ObjectMeta.Name, err)
		}

		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if endpoint.Spec.ClientTLS != nil && endpoint.Spec.ClientTLS.Renegotiation != nil {
		switch *endpoint.Spec.ClientTLS.Renegotiation {
		case commonapi.NegotiateNever:
			tlsConfig.Renegotiation = tls.RenegotiateNever
		case commonapi.NegotiateOnceAsClient:
			tlsConfig.Renegotiation = tls.RenegotiateOnceAsClient
		case commonapi.NegotiateFreelyAsClient:
			tlsConfig.Renegotiation = tls.RenegotiateFreelyAsClient
		default:
			return httpChannel.ChannelConfiguration{}, fmt.Errorf("invalid renegotiation value %s for http endpoint %s", *endpoint.Spec.ClientTLS.Renegotiation, endpoint.ObjectMeta.Name)
		}
	}

	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
	}

	tr := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.TLSClientConfig = tlsConfig
	tr.DialContext = dialer.DialContext

	conf.Client = &nethttp.Client{
		Timeout:   0,
		Transport: tr,
	}

	return conf, nil
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

func (a *DaprRuntime) startSubscriptions() {
	// Clean any previous state
	if a.pubsubCancel != nil {
		a.stopSubscriptions() // Stop all subscriptions
	}

	// PubSub subscribers are stopped via cancellation of the main runtime's context
	a.pubsubCtx, a.pubsubCancel = context.WithCancel(a.ctx)
	a.topicCtxCancels = map[string]context.CancelFunc{}
	for pubsubName := range a.compStore.ListPubSubs() {
		if err := a.beginPubSub(pubsubName); err != nil {
			log.Errorf("error occurred while beginning pubsub %s: %v", pubsubName, err)
		}
	}
}

// Stop subscriptions to all topics and cleans the cached topics
func (a *DaprRuntime) stopSubscriptions() {
	if a.pubsubCtx == nil || a.pubsubCtx.Err() != nil { // no pubsub to stop
		return
	}
	if a.pubsubCancel != nil {
		a.pubsubCancel() // Stop all subscriptions by canceling the subscription context
	}

	// Remove all contexts that are specific to each component (which have been canceled already by canceling pubsubCtx)
	a.topicCtxCancels = nil

	// Delete the cached topics and routes
	a.compStore.SetTopicRoutes(nil)
}

func (a *DaprRuntime) startReadingFromBindings() (err error) {
	if a.appChannel == nil {
		return errors.New("app channel not initialized")
	}

	// Clean any previous state
	if a.inputBindingsCancel != nil {
		a.inputBindingsCancel()
	}

	// Input bindings are stopped via cancellation of the main runtime's context
	a.inputBindingsCtx, a.inputBindingsCancel = context.WithCancel(a.ctx)

	comps := a.compStore.ListComponents()
	bindings := make(map[string]componentsV1alpha1.Component)
	for i, c := range comps {
		if strings.HasPrefix(c.Spec.Type, string(components.CategoryBindings)) {
			bindings[c.ObjectMeta.Name] = comps[i]
		}
	}

	for name, binding := range a.compStore.ListInputBindings() {
		var isSubscribed bool

		m := a.meta.ToBaseMetadata(bindings[name]).Properties

		if isBindingOfExplicitDirection(processor.BindingTypeInput, m) {
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

		err = a.readFromBinding(a.inputBindingsCtx, name, binding)
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

// Returns "componentName||topicName", which is used as key for some maps
func pubsubTopicKey(componentName, topicName string) string {
	return componentName + "||" + topicName
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

func (a *DaprRuntime) stopTrace() {
	if a.tracerProvider == nil {
		return
	}
	// Flush and shutdown the tracing provider.
	shutdownCtx, shutdownCancel := context.WithCancel(a.ctx)
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

func (a *DaprRuntime) createHTTPEndpointsChannels() (map[string]channel.HTTPEndpointAppChannel, error) {
	// Create dedicated app channels for known app endpoints
	endpoints := a.compStore.ListHTTPEndpoints()

	if len(endpoints) > 0 {
		channels := make(map[string]channel.HTTPEndpointAppChannel, len(endpoints))

		pipeline, err := a.buildAppHTTPPipeline()
		if err != nil {
			return nil, err
		}

		for _, e := range endpoints {
			conf, err := a.getHTTPEndpointAppChannel(pipeline, e)
			if err != nil {
				return nil, err
			}

			ch, err := httpChannel.CreateHTTPChannel(conf)
			if err != nil {
				return nil, err
			}

			channels[e.ObjectMeta.Name] = ch
		}

		return channels, nil
	}

	return nil, nil
}

func (a *DaprRuntime) createChannels() (err error) {
	// Create a HTTP channel for external HTTP endpoint invocation
	pipeline, err := a.buildAppHTTPPipeline()
	if err != nil {
		return fmt.Errorf("failed to build app HTTP pipeline: %w", err)
	}

	a.httpEndpointsAppChannel, err = httpChannel.CreateHTTPChannel(a.getAppHTTPChannelConfig(pipeline))
	if err != nil {
		return fmt.Errorf("failed to create external HTTP app channel: %w", err)
	}

	if a.runtimeConfig.appConnectionConfig.Port == 0 {
		log.Warn("App channel is not initialized. Did you configure an app-port?")
		return nil
	}

	if a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
		// Create a HTTP channel
		a.appChannel, err = httpChannel.CreateHTTPChannel(a.getAppHTTPChannelConfig(pipeline))
		if err != nil {
			return fmt.Errorf("failed to create HTTP app channel: %w", err)
		}
		a.appChannel.(*httpChannel.Channel).SetAppHealthCheckPath(a.runtimeConfig.appConnectionConfig.HealthCheckHTTPPath)
	} else {
		// create gRPC app channel
		a.appChannel, err = a.grpc.GetAppChannel()
		if err != nil {
			return fmt.Errorf("failed to create gRPC app channel: %w", err)
		}
	}

	return nil
}
