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
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	md "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	httpChannel "github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/resiliency"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	wfs "github.com/dapr/components-contrib/workflows"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	cryptoLoader "github.com/dapr/dapr/pkg/components/crypto"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	"github.com/dapr/dapr/pkg/components/pluggable"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	workflowsLoader "github.com/dapr/dapr/pkg/components/workflows"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/contenttype"
	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/components-contrib/lock"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/middleware"
	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
)

const (
	actorStateStore = "actorStateStore"

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

type ComponentCategory string

var componentCategoriesNeedProcess = []components.Category{
	components.CategoryBindings,
	components.CategoryPubSub,
	components.CategorySecretStore,
	components.CategoryStateStore,
	components.CategoryMiddleware,
	components.CategoryConfiguration,
	components.CategoryCryptoProvider,
	components.CategoryLock,
	components.CategoryWorkflow,
}

var log = logger.NewLogger("dapr.runtime")

// ErrUnexpectedEnvelopeData denotes that an unexpected data type
// was encountered when processing a cloud event's data property.
var ErrUnexpectedEnvelopeData = errors.New("unexpected data type encountered in envelope")

var cloudEventDuplicateKeys = sets.NewString(pubsub.IDField, pubsub.SourceField, pubsub.DataContentTypeField, pubsub.TypeField, pubsub.SpecVersionField, pubsub.DataField, pubsub.DataBase64Field)

type TopicRoutes map[string]TopicRouteElem

type TopicRouteElem struct {
	metadata        map[string]string
	rules           []*runtimePubsub.Rule
	deadLetterTopic string
	bulkSubscribe   *runtimePubsub.BulkSubscribe
}

// Type of function that determines if a component is authorized.
// The function receives the component and must return true if the component is authorized.
type ComponentAuthorizer func(component componentsV1alpha1.Component) bool

// DaprRuntime holds all the core components of the runtime.
type DaprRuntime struct {
	ctx                       context.Context
	cancel                    context.CancelFunc
	runtimeConfig             *Config
	globalConfig              *config.Configuration
	accessControlList         *config.AccessControlList
	componentsLock            *sync.RWMutex
	components                []componentsV1alpha1.Component
	grpc                      *grpc.Manager
	appChannel                channel.AppChannel
	appConfig                 config.ApplicationConfig
	directMessaging           messaging.DirectMessaging
	stateStoreRegistry        *stateLoader.Registry
	secretStoresRegistry      *secretstoresLoader.Registry
	nameResolutionRegistry    *nrLoader.Registry
	workflowComponentRegistry *workflowsLoader.Registry
	stateStores               map[string]state.Store
	actor                     actors.Actors
	bindingsRegistry          *bindingsLoader.Registry
	subscribeBindingList      []string
	inputBindings             map[string]bindings.InputBinding
	outputBindings            map[string]bindings.OutputBinding
	inputBindingsCtx          context.Context
	inputBindingsCancel       context.CancelFunc
	secretStores              map[string]secretstores.SecretStore
	pubSubRegistry            *pubsubLoader.Registry
	pubSubs                   map[string]pubsubItem // Key is "componentName"
	workflowComponents        map[string]wfs.Workflow
	nameResolver              nr.Resolver
	httpMiddlewareRegistry    *httpMiddlewareLoader.Registry
	hostAddress               string
	actorStateStoreName       string
	actorStateStoreLock       *sync.RWMutex
	authenticator             security.Authenticator
	namespace                 string
	podName                   string
	daprHTTPAPI               http.API
	daprGRPCAPI               grpc.API
	operatorClient            operatorv1pb.OperatorClient
	pubsubCtx                 context.Context
	pubsubCancel              context.CancelFunc
	topicsLock                *sync.RWMutex
	topicRoutes               map[string]TopicRoutes        // Key is "componentName"
	topicCtxCancels           map[string]context.CancelFunc // Key is "componentName||topicName"
	subscriptions             []runtimePubsub.Subscription
	inputBindingRoutes        map[string]string
	shutdownC                 chan error
	running                   atomic.Bool
	apiClosers                []io.Closer
	componentAuthorizers      []ComponentAuthorizer
	appHealth                 *apphealth.AppHealth
	appHealthReady            func() // Invoked the first time the app health becomes ready
	appHealthLock             *sync.Mutex
	bulkSubLock               *sync.Mutex

	secretsConfiguration map[string]config.SecretsScope

	configurationStoreRegistry *configurationLoader.Registry
	configurationStores        map[string]configuration.Store

	lockStoreRegistry *lockLoader.Registry
	lockStores        map[string]lock.Store

	cryptoProviderRegistry *cryptoLoader.Registry
	cryptoProviders        map[string]contribCrypto.SubtleCrypto

	pendingComponents          chan componentsV1alpha1.Component
	pendingComponentDependents map[string][]componentsV1alpha1.Component

	proxy messaging.Proxy

	resiliency resiliency.Provider

	tracerProvider *sdktrace.TracerProvider

	workflowEngine *wfengine.WorkflowEngine
}

type ComponentsCallback func(components ComponentRegistry) error

type ComponentRegistry struct {
	Actors          actors.Actors
	DirectMessaging messaging.DirectMessaging
	StateStores     map[string]state.Store
	InputBindings   map[string]bindings.InputBinding
	OutputBindings  map[string]bindings.OutputBinding
	SecretStores    map[string]secretstores.SecretStore
	PubSubs         map[string]pubsub.PubSub
	CryptoProviders map[string]contribCrypto.SubtleCrypto
	Workflows       map[string]wfs.Workflow
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

type pubsubItem struct {
	component           pubsub.PubSub
	scopedSubscriptions []string
	scopedPublishings   []string
	allowedTopics       []string
	namespaceScoped     bool
}

// NewDaprRuntime returns a new runtime with the given runtime config and global config.
func NewDaprRuntime(runtimeConfig *Config, globalConfig *config.Configuration, accessControlList *config.AccessControlList, resiliencyProvider resiliency.Provider) *DaprRuntime {
	ctx, cancel := context.WithCancel(context.Background())

	rt := &DaprRuntime{
		ctx:                        ctx,
		cancel:                     cancel,
		runtimeConfig:              runtimeConfig,
		globalConfig:               globalConfig,
		accessControlList:          accessControlList,
		componentsLock:             &sync.RWMutex{},
		components:                 make([]componentsV1alpha1.Component, 0),
		actorStateStoreLock:        &sync.RWMutex{},
		grpc:                       createGRPCManager(runtimeConfig, globalConfig),
		inputBindings:              map[string]bindings.InputBinding{},
		outputBindings:             map[string]bindings.OutputBinding{},
		secretStores:               map[string]secretstores.SecretStore{},
		stateStores:                map[string]state.Store{},
		pubSubs:                    map[string]pubsubItem{},
		topicsLock:                 &sync.RWMutex{},
		inputBindingRoutes:         map[string]string{},
		secretsConfiguration:       map[string]config.SecretsScope{},
		configurationStores:        map[string]configuration.Store{},
		lockStores:                 map[string]lock.Store{},
		cryptoProviders:            map[string]contribCrypto.SubtleCrypto{},
		workflowComponents:         map[string]wfs.Workflow{},
		pendingComponents:          make(chan componentsV1alpha1.Component),
		pendingComponentDependents: map[string][]componentsV1alpha1.Component{},
		shutdownC:                  make(chan error, 1),
		tracerProvider:             nil,
		resiliency:                 resiliencyProvider,
		workflowEngine:             wfengine.NewWorkflowEngine(),
		appHealthReady:             nil,
		appHealthLock:              &sync.Mutex{},
		bulkSubLock:                &sync.Mutex{},
	}

	rt.componentAuthorizers = []ComponentAuthorizer{rt.namespaceComponentAuthorizer}
	if globalConfig != nil && len(globalConfig.Spec.ComponentsSpec.Deny) > 0 {
		dl := newComponentDenyList(globalConfig.Spec.ComponentsSpec.Deny)
		rt.componentAuthorizers = append(rt.componentAuthorizers, dl.IsAllowed)
	}

	return rt
}

// Run performs initialization of the runtime with the runtime and global configurations.
func (a *DaprRuntime) Run(opts ...Option) error {
	start := time.Now()
	log.Infof("%s mode configured", a.runtimeConfig.Mode)
	log.Infof("app id: %s", a.runtimeConfig.ID)

	var o runtimeOpts
	for _, opt := range opts {
		opt(&o)
	}

	if err := a.initRuntime(&o); err != nil {
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

func (a *DaprRuntime) getNamespace() string {
	return os.Getenv("NAMESPACE")
}

func (a *DaprRuntime) getPodName() string {
	return os.Getenv("POD_NAME")
}

func (a *DaprRuntime) getOperatorClient() (operatorv1pb.OperatorClient, error) {
	if a.runtimeConfig.Mode == modes.KubernetesMode {
		client, _, err := client.GetOperatorClient(a.runtimeConfig.Kubernetes.ControlPlaneAddress, security.TLSServerName, a.runtimeConfig.CertChain)
		if err != nil {
			return nil, fmt.Errorf("error creating operator client: %w", err)
		}
		return client, nil
	}
	return nil, nil
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
		otelExporter, err := otlptrace.New(a.ctx, client)
		if err != nil {
			return err
		}
		tpStore.RegisterExporter(otelExporter)
	}

	// Register a resource
	r := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(a.runtimeConfig.ID),
	)

	tpStore.RegisterResource(r)

	// Register a trace sampler based on Sampling settings
	daprTraceSampler := diag.NewDaprTraceSampler(tracingSpec.SamplingRate)
	log.Infof("Dapr trace sampler initialized: %s", daprTraceSampler.Description())

	tpStore.RegisterSampler(daprTraceSampler)

	a.tracerProvider = tpStore.RegisterTracerProvider()
	return nil
}

func (a *DaprRuntime) initRuntime(opts *runtimeOpts) error {
	a.namespace = a.getNamespace()

	err := a.establishSecurity(a.runtimeConfig.SentryServiceAddress)
	if err != nil {
		return err
	}
	a.podName = a.getPodName()
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
	a.nameResolutionRegistry = opts.nameResolutionRegistry
	err = a.initNameResolution()
	if err != nil {
		log.Errorf(err.Error())
	}

	a.pubSubRegistry = opts.pubsubRegistry
	a.secretStoresRegistry = opts.secretStoreRegistry
	a.stateStoreRegistry = opts.stateRegistry
	a.configurationStoreRegistry = opts.configurationRegistry
	a.bindingsRegistry = opts.bindingRegistry
	a.cryptoProviderRegistry = opts.cryptoProviderRegistry
	a.httpMiddlewareRegistry = opts.httpMiddlewareRegistry
	a.lockStoreRegistry = opts.lockRegistry
	a.workflowComponentRegistry = opts.workflowComponentRegistry

	a.initPluggableComponents()

	go a.processComponents()

	if _, ok := os.LookupEnv(hotReloadingEnvVar); ok {
		log.Debug("starting to watch component updates")
		err = a.beginComponentsUpdates()
		if err != nil {
			log.Warnf("failed to watch component updates: %s", err)
		}
	}

	a.appendBuiltinSecretStore()
	err = a.loadComponents(opts)
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}

	a.flushOutstandingComponents()

	pipeline, err := a.buildHTTPPipeline()
	if err != nil {
		log.Warnf("failed to build HTTP pipeline: %s", err)
	}

	// Setup allow/deny list for secrets
	a.populateSecretsConfiguration()

	// Start proxy
	a.initProxy()

	// Create and start internal and external gRPC servers
	a.daprGRPCAPI = a.getGRPCAPI()

	err = a.startGRPCAPIServer(a.daprGRPCAPI, a.runtimeConfig.APIGRPCPort)
	if err != nil {
		log.Fatalf("failed to start API gRPC server: %s", err)
	}
	if a.runtimeConfig.UnixDomainSocket != "" {
		log.Info("API gRPC server is running on a unix domain socket")
	} else {
		log.Infof("API gRPC server is running on port %v", a.runtimeConfig.APIGRPCPort)
	}

	a.initDirectMessaging(a.nameResolver)

	// Start HTTP Server
	err = a.startHTTPServer(a.runtimeConfig.HTTPPort, a.runtimeConfig.PublicPort, a.runtimeConfig.ProfilePort, a.runtimeConfig.AllowedOrigins, pipeline)
	if err != nil {
		log.Fatalf("failed to start HTTP server: %s", err)
	}
	if a.runtimeConfig.UnixDomainSocket != "" {
		log.Info("http server is running on a unix domain socket")
	} else {
		log.Infof("http server is running on port %v", a.runtimeConfig.HTTPPort)
	}
	log.Infof("The request body size parameter is: %v", a.runtimeConfig.MaxRequestBodySize)

	err = a.startGRPCInternalServer(a.daprGRPCAPI, a.runtimeConfig.InternalGRPCPort)
	if err != nil {
		log.Fatalf("failed to start internal gRPC server: %s", err)
	}
	log.Infof("internal gRPC server is running on port %v", a.runtimeConfig.InternalGRPCPort)

	if a.daprHTTPAPI != nil {
		a.daprHTTPAPI.MarkStatusAsOutboundReady()
	}

	a.blockUntilAppIsReady()

	err = a.createAppChannel()
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.ApplicationProtocol), err)
	}
	a.daprHTTPAPI.SetAppChannel(a.appChannel)
	a.daprGRPCAPI.SetAppChannel(a.appChannel)
	a.directMessaging.SetAppChannel(a.appChannel)

	a.initDirectMessaging(a.nameResolver)

	a.daprHTTPAPI.SetDirectMessaging(a.directMessaging)
	a.daprGRPCAPI.SetDirectMessaging(a.directMessaging)

	if a.runtimeConfig.MaxConcurrency > 0 {
		log.Infof("app max concurrency set to %v", a.runtimeConfig.MaxConcurrency)
	}

	a.appHealthReady = func() {
		a.appHealthReadyInit(opts)
	}
	if a.runtimeConfig.AppHealthCheck != nil && a.appChannel != nil {
		// We can't just pass "a.appChannel.HealthProbe" because appChannel may be re-created
		a.appHealth = apphealth.NewAppHealth(a.runtimeConfig.AppHealthCheck, func(ctx context.Context) (bool, error) {
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
func (a *DaprRuntime) appHealthReadyInit(opts *runtimeOpts) {
	var err error

	// Load app configuration (for actors) and init actors
	a.loadAppConfiguration()

	if len(a.runtimeConfig.PlacementAddresses) != 0 {
		err = a.initActors()
		if err != nil {
			log.Warnf(err.Error())
		} else {
			a.daprHTTPAPI.SetActorRuntime(a.actor)
			a.daprGRPCAPI.SetActorRuntime(a.actor)

			// Workflow engine depends on actor runtime being initialized
			a.initWorkflowEngine()
		}
	}

	if opts.componentsCallback != nil {
		pubsubs := make(map[string]pubsub.PubSub, len(a.pubSubs))
		for k, v := range a.pubSubs {
			pubsubs[k] = v.component
		}
		if err = opts.componentsCallback(ComponentRegistry{
			Actors:          a.actor,
			DirectMessaging: a.directMessaging,
			StateStores:     a.stateStores,
			InputBindings:   a.inputBindings,
			OutputBindings:  a.outputBindings,
			SecretStores:    a.secretStores,
			PubSubs:         pubsubs,
			Workflows:       a.workflowComponents,
		}); err != nil {
			log.Fatalf("failed to register components with callback: %s", err)
		}
	}
}

func (a *DaprRuntime) initWorkflowEngine() {
	wfComponentFactory := wfengine.BuiltinWorkflowFactory(a.workflowEngine)

	if wfInitErr := a.workflowEngine.SetActorRuntime(a.actor); wfInitErr != nil {
		log.Warnf("Failed to set actor runtime for Dapr workflow engine - workflow engine will not start: %w", wfInitErr)
	} else {
		if a.workflowComponentRegistry != nil {
			log.Infof("Registering component for dapr workflow engine...")
			a.workflowComponentRegistry.RegisterComponent(wfComponentFactory, "dapr")
			if componentInitErr := a.initWorkflowComponent(wfengine.ComponentDefinition); componentInitErr != nil {
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
	for _, scope := range a.globalConfig.Spec.Secrets.Scopes {
		a.secretsConfiguration[scope.StoreName] = scope
	}
}

func (a *DaprRuntime) buildHTTPPipelineForSpec(spec config.PipelineSpec, targetPipeline string) (httpMiddleware.Pipeline, error) {
	var handlers []httpMiddleware.Middleware

	if a.globalConfig != nil {
		for i := 0; i < len(spec.Handlers); i++ {
			middlewareSpec := spec.Handlers[i]
			component, exists := a.getComponent(middlewareSpec.Type, middlewareSpec.Name)
			if !exists {
				err := fmt.Errorf(
					"couldn't find middleware component with name %s and type %s/%s",
					middlewareSpec.Name, middlewareSpec.Type, middlewareSpec.Version,
				)
				return httpMiddleware.Pipeline{}, err
			}
			md := middleware.Metadata{Base: a.toBaseMetadata(component)}
			handler, err := a.httpMiddlewareRegistry.Create(middlewareSpec.Type, middlewareSpec.Version, md, middlewareSpec.LogName())
			if err != nil {
				return httpMiddleware.Pipeline{}, err
			}
			log.Infof("enabled %s/%s %s middleware", middlewareSpec.Type, targetPipeline, middlewareSpec.Version)
			handlers = append(handlers, handler)
		}
	}
	return httpMiddleware.Pipeline{Handlers: handlers}, nil
}

func (a *DaprRuntime) buildHTTPPipeline() (httpMiddleware.Pipeline, error) {
	return a.buildHTTPPipelineForSpec(a.globalConfig.Spec.HTTPPipelineSpec, "http")
}

func (a *DaprRuntime) buildAppHTTPPipeline() (httpMiddleware.Pipeline, error) {
	return a.buildHTTPPipelineForSpec(a.globalConfig.Spec.AppHTTPPipelineSpec, "app channel")
}

func (a *DaprRuntime) initBinding(c componentsV1alpha1.Component) error {
	if a.bindingsRegistry.HasOutputBinding(c.Spec.Type, c.Spec.Version) {
		if err := a.initOutputBinding(c); err != nil {
			return err
		}
	}

	if a.bindingsRegistry.HasInputBinding(c.Spec.Type, c.Spec.Version) {
		if err := a.initInputBinding(c); err != nil {
			return err
		}
	}
	return nil
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

func (a *DaprRuntime) subscribeTopic(parentCtx context.Context, name string, topic string, route TopicRouteElem) error {
	subKey := pubsubTopicKey(name, topic)

	a.topicsLock.Lock()
	defer a.topicsLock.Unlock()

	allowed := a.isPubSubOperationAllowed(name, topic, a.pubSubs[name].scopedSubscriptions)
	if !allowed {
		return fmt.Errorf("subscription to topic '%s' on pubsub '%s' is not allowed", topic, name)
	}

	log.Debugf("subscribing to topic='%s' on pubsub='%s'", topic, name)

	if _, ok := a.topicCtxCancels[subKey]; ok {
		return fmt.Errorf("cannot subscribe to topic '%s' on pubsub '%s': the subscription already exists", topic, name)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	policyDef := a.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.metadata

	namespaced := a.pubSubs[name].namespaceScoped

	if route.bulkSubscribe != nil && route.bulkSubscribe.Enabled {
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

	err := a.pubSubs[name].component.Subscribe(ctx, pubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *pubsub.NewMessage) error {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[pubsubName] = name

		msgTopic := msg.Topic
		if a.pubSubs[name].namespaceScoped {
			msgTopic = strings.Replace(msgTopic, a.namespace, "", 1)
		}

		rawPayload, err := contribMetadata.IsRawPayload(route.metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if route.deadLetterTopic != "" {
				if dlqErr := a.sendToDeadLetter(name, msg, route.deadLetterTopic); dlqErr == nil {
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
				if route.deadLetterTopic != "" {
					if dlqErr := a.sendToDeadLetter(name, msg, route.deadLetterTopic); dlqErr == nil {
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
				if route.deadLetterTopic != "" {
					if dlqErr := a.sendToDeadLetter(name, msg, route.deadLetterTopic); dlqErr == nil {
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

			if route.deadLetterTopic != "" {
				_ = a.sendToDeadLetter(name, msg, route.deadLetterTopic)
			}
			return nil
		}

		routePath, shouldProcess, err := findMatchingRoute(route.rules, cloudEvent)
		if err != nil {
			log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[pubsub.IDField], name, msgTopic, err)
			if route.deadLetterTopic != "" {
				if dlqErr := a.sendToDeadLetter(name, msg, route.deadLetterTopic); dlqErr == nil {
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
			if route.deadLetterTopic != "" {
				_ = a.sendToDeadLetter(name, msg, route.deadLetterTopic)
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
			switch a.runtimeConfig.ApplicationProtocol {
			case HTTPProtocol:
				pErr = a.publishMessageHTTP(ctx, psm)
			case GRPCProtocol:
				pErr = a.publishMessageGRPC(ctx, psm)
			default:
				pErr = backoff.Permanent(errors.New("invalid application protocol"))
			}
			return nil, pErr
		})
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.deadLetterTopic == "" {
				return err
			}
			_ = a.sendToDeadLetter(name, msg, route.deadLetterTopic)
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
		AppID:              a.runtimeConfig.ID,
		Namespace:          a.namespace,
		Port:               a.runtimeConfig.InternalGRPCPort,
		Mode:               a.runtimeConfig.Mode,
		AppChannel:         a.appChannel,
		ClientConnFn:       a.grpc.GetGRPCConnection,
		Resolver:           resolver,
		MaxRequestBodySize: a.runtimeConfig.MaxRequestBodySize,
		Proxy:              a.proxy,
		ReadBufferSize:     a.runtimeConfig.ReadBufferSize,
		Resiliency:         a.resiliency,
		IsStreamingEnabled: a.globalConfig.IsFeatureEnabled(config.ServiceInvocationStreaming),
	})
}

func (a *DaprRuntime) initProxy() {
	a.proxy = messaging.NewProxy(messaging.ProxyOpts{
		AppClientFn:       a.grpc.GetAppClient,
		ConnectionFactory: a.grpc.GetGRPCConnection,
		AppID:             a.runtimeConfig.ID,
		ACL:               a.accessControlList,
		Resiliency:        a.resiliency,
	})

	log.Info("gRPC proxy enabled")
}

// begin components updates for kubernetes mode.
func (a *DaprRuntime) beginComponentsUpdates() error {
	if a.runtimeConfig.Mode != modes.KubernetesMode {
		return nil
	}

	go func() {
		parseAndUpdate := func(compRaw []byte) {
			var component componentsV1alpha1.Component
			if err := json.Unmarshal(compRaw, &component); err != nil {
				log.Warnf("error deserializing component: %s", err)
				return
			}

			if !a.isComponentAuthorized(component) {
				log.Debugf("received unauthorized component update, ignored. name: %s, type: %s/%s", component.ObjectMeta.Name, component.Spec.Type, component.Spec.Version)
				return
			}

			log.Debugf("received component update. name: %s, type: %s/%s", component.ObjectMeta.Name, component.Spec.Type, component.Spec.Version)
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
	oldComp, exists := a.getComponent(component.Spec.Type, component.Name)
	newComp, _ := a.processComponentSecrets(component)

	if exists && reflect.DeepEqual(oldComp.Spec, newComp.Spec) {
		return false
	}

	a.pendingComponents <- component
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

	if binding, ok := a.outputBindings[name]; ok {
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
			if a.stateStores != nil {
				policyRunner := resiliency.NewRunner[any](a.ctx,
					a.resiliency.ComponentOutboundPolicy(response.StoreName, resiliency.Statestore),
				)
				_, err := policyRunner(func(ctx context.Context) (any, error) {
					return nil, a.stateStores[response.StoreName].BulkSet(ctx, reqs)
				})
				if err != nil {
					log.Errorf("error saving state from app response: %s", err)
				}
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
	spanName := fmt.Sprintf("bindings/%s", bindingName)
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
	path := a.inputBindingRoutes[bindingName]
	if path == "" {
		path = bindingName
	}

	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
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
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
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
			rResp, rErr := a.appChannel.InvokeMethod(ctx, req)
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
		AppID:                       a.runtimeConfig.ID,
		AppChannel:                  a.appChannel,
		DirectMessaging:             a.directMessaging,
		GetComponentsFn:             a.getComponents,
		GetSubscriptionsFn:          a.getSubscriptions,
		Resiliency:                  a.resiliency,
		StateStores:                 a.stateStores,
		WorkflowsComponents:         a.workflowComponents,
		LockStores:                  a.lockStores,
		CryptoProviders:             a.cryptoProviders,
		SecretStores:                a.secretStores,
		SecretsConfiguration:        a.secretsConfiguration,
		ConfigurationStores:         a.configurationStores,
		PubsubAdapter:               a.getPublishAdapter(),
		Actor:                       a.actor,
		SendToOutputBindingFn:       a.sendToOutputBinding,
		TracingSpec:                 a.globalConfig.Spec.TracingSpec,
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		MaxRequestBodySize:          int64(a.runtimeConfig.MaxRequestBodySize) << 20, // Convert from MB to bytes
	})

	serverConf := http.ServerConfig{
		AppID:                   a.runtimeConfig.ID,
		HostAddress:             a.hostAddress,
		Port:                    port,
		APIListenAddresses:      a.runtimeConfig.APIListenAddresses,
		PublicPort:              publicPort,
		ProfilePort:             profilePort,
		AllowedOrigins:          allowedOrigins,
		EnableProfiling:         a.runtimeConfig.EnableProfiling,
		MaxRequestBodySize:      a.runtimeConfig.MaxRequestBodySize,
		UnixDomainSocket:        a.runtimeConfig.UnixDomainSocket,
		ReadBufferSize:          a.runtimeConfig.ReadBufferSize,
		EnableAPILogging:        a.runtimeConfig.EnableAPILogging,
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
	serverConf := a.getNewServerConfig(a.runtimeConfig.APIListenAddresses, port)
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
		AppID:                a.runtimeConfig.ID,
		HostAddress:          a.hostAddress,
		Port:                 port,
		APIListenAddresses:   apiListenAddresses,
		NameSpace:            a.namespace,
		TrustDomain:          trustDomain,
		MaxRequestBodySizeMB: a.runtimeConfig.MaxRequestBodySize,
		UnixDomainSocket:     a.runtimeConfig.UnixDomainSocket,
		ReadBufferSizeKB:     a.runtimeConfig.ReadBufferSize,
		EnableAPILogging:     a.runtimeConfig.EnableAPILogging,
	}
}

func (a *DaprRuntime) getGRPCAPI() grpc.API {
	return grpc.NewAPI(grpc.APIOpts{
		AppID:                       a.runtimeConfig.ID,
		AppChannel:                  a.appChannel,
		Resiliency:                  a.resiliency,
		StateStores:                 a.stateStores,
		SecretStores:                a.secretStores,
		WorkflowComponents:          a.workflowComponents,
		SecretsConfiguration:        a.secretsConfiguration,
		ConfigurationStores:         a.configurationStores,
		CryptoProviders:             a.cryptoProviders,
		LockStores:                  a.lockStores,
		PubsubAdapter:               a.getPublishAdapter(),
		DirectMessaging:             a.directMessaging,
		Actor:                       a.actor,
		SendToOutputBindingFn:       a.sendToOutputBinding,
		TracingSpec:                 a.globalConfig.Spec.TracingSpec,
		AccessControlList:           a.accessControlList,
		AppProtocol:                 string(a.runtimeConfig.ApplicationProtocol),
		Shutdown:                    a.ShutdownWithWait,
		GetComponentsFn:             a.getComponents,
		GetComponentsCapabilitiesFn: a.getComponentsCapabilitesMap,
		GetSubscriptionsFn:          a.getSubscriptions,
	})
}

func (a *DaprRuntime) getPublishAdapter() runtimePubsub.Adapter {
	if len(a.pubSubs) == 0 {
		return nil
	}

	return a
}

func (a *DaprRuntime) getSubscribedBindingsGRPC() ([]string, error) {
	conn, err := a.grpc.GetAppClient()
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
	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
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
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		// if HTTP, check if there's an endpoint listening for that binding
		path := a.inputBindingRoutes[binding]
		req := invokev1.NewInvokeMethodRequest(path).
			WithHTTPExtension(nethttp.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer req.Close()

		// TODO: Propagate Context
		resp, err := a.appChannel.InvokeMethod(context.TODO(), req)
		if err != nil {
			log.Fatalf("could not invoke OPTIONS method on input binding subscription endpoint %q: %v", path, err)
		}
		defer resp.Close()
		code := resp.Status().Code

		return code/100 == 2 || code == nethttp.StatusMethodNotAllowed, nil
	}
	return false, nil
}

func (a *DaprRuntime) initInputBinding(c componentsV1alpha1.Component) error {
	fName := c.LogName()
	binding, err := a.bindingsRegistry.CreateInputBinding(c.Spec.Type, c.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation", c.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}
	err = binding.Init(context.TODO(), bindings.Metadata{Base: a.toBaseMetadata(c)})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init", c.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}

	log.Infof("successful init for input binding %s (%s/%s)", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
	a.inputBindingRoutes[c.Name] = c.Name
	for _, item := range c.Spec.Metadata {
		if item.Name == "route" {
			a.inputBindingRoutes[c.ObjectMeta.Name] = item.Value.String()
		}
	}
	a.inputBindings[c.Name] = binding
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) initOutputBinding(c componentsV1alpha1.Component) error {
	fName := c.LogName()
	binding, err := a.bindingsRegistry.CreateOutputBinding(c.Spec.Type, c.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation", c.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}

	if binding != nil {
		err := binding.Init(context.TODO(), bindings.Metadata{Base: a.toBaseMetadata(c)})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init", c.ObjectMeta.Name)
			return NewInitError(InitComponentFailure, fName, err)
		}
		log.Infof("successful init for output binding %s (%s/%s)", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
		a.outputBindings[c.ObjectMeta.Name] = binding
		diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	}
	return nil
}

func (a *DaprRuntime) initConfiguration(s componentsV1alpha1.Component) error {
	fName := s.LogName()
	store, err := a.configurationStoreRegistry.Create(s.Spec.Type, s.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation", s.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}
	if store != nil {
		err := store.Init(context.TODO(), configuration.Metadata{Base: a.toBaseMetadata(s)})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
			return NewInitError(InitComponentFailure, fName, err)
		}

		a.configurationStores[s.ObjectMeta.Name] = store
		diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)
	}

	return nil
}

func (a *DaprRuntime) initLock(s componentsV1alpha1.Component) error {
	// create the component
	fName := s.LogName()
	store, err := a.lockStoreRegistry.Create(s.Spec.Type, s.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation", s.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}
	if store == nil {
		return nil
	}
	// initialization
	baseMetadata := a.toBaseMetadata(s)
	props := baseMetadata.Properties
	err = store.InitLockStore(context.TODO(), lock.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}
	// save lock related configuration
	a.lockStores[s.ObjectMeta.Name] = store
	err = lockLoader.SaveLockConfiguration(s.ObjectMeta.Name, props)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
		wrapError := fmt.Errorf("failed to save lock keyprefix: %s", err.Error())
		return NewInitError(InitComponentFailure, fName, wrapError)
	}
	diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)

	return nil
}

func (a *DaprRuntime) initWorkflowComponent(s componentsV1alpha1.Component) error {
	// create the component
	fName := s.LogName()
	workflowComp, err := a.workflowComponentRegistry.Create(s.Spec.Type, s.Spec.Version, fName)
	if err != nil {
		log.Warnf("error creating workflow component %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
		return err
	}

	if workflowComp == nil {
		return nil
	}

	// initialization
	baseMetadata := a.toBaseMetadata(s)
	err = workflowComp.Init(wfs.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}
	// save workflow related configuration
	a.workflowComponents[s.ObjectMeta.Name] = workflowComp
	diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)

	return nil
}

// Refer for state store api decision  https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (a *DaprRuntime) initState(s componentsV1alpha1.Component) error {
	fName := s.LogName()
	store, err := a.stateStoreRegistry.Create(s.Spec.Type, s.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation", s.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}
	if store != nil {
		secretStoreName := a.authSecretStoreOrDefault(s)

		secretStore := a.getSecretStore(secretStoreName)
		encKeys, encErr := encryption.ComponentEncryptionKey(s, secretStore)
		if encErr != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation", s.ObjectMeta.Name)
			return NewInitError(CreateComponentFailure, fName, err)
		}

		if encKeys.Primary.Key != "" {
			ok := encryption.AddEncryptedStateStore(s.ObjectMeta.Name, encKeys)
			if ok {
				log.Infof("automatic encryption enabled for state store %s", s.ObjectMeta.Name)
			}
		}

		baseMetadata := a.toBaseMetadata(s)
		props := baseMetadata.Properties
		err = store.Init(context.TODO(), state.Metadata{Base: baseMetadata})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
			return NewInitError(InitComponentFailure, fName, err)
		}

		a.stateStores[s.ObjectMeta.Name] = store
		err = stateLoader.SaveStateConfiguration(s.ObjectMeta.Name, props)
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init", s.ObjectMeta.Name)
			wrapError := fmt.Errorf("failed to save lock keyprefix: %s", err.Error())
			return NewInitError(InitComponentFailure, fName, wrapError)
		}

		// when placement address list is not empty, set specified actor store.
		if len(a.runtimeConfig.PlacementAddresses) != 0 {
			// set specified actor store if "actorStateStore" is true in the spec.
			actorStoreSpecified := props[actorStateStore]
			if actorStoreSpecified == "true" {
				a.actorStateStoreLock.Lock()
				if a.actorStateStoreName == "" {
					log.Infof("detected actor state store: %s", s.ObjectMeta.Name)
					a.actorStateStoreName = s.ObjectMeta.Name
				} else if a.actorStateStoreName != s.ObjectMeta.Name {
					log.Fatalf("detected duplicate actor state store: %s", s.ObjectMeta.Name)
				}
				a.actorStateStoreLock.Unlock()
			}
		}
		diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)
	}

	return nil
}

func (a *DaprRuntime) getDeclarativeSubscriptions() []runtimePubsub.Subscription {
	var subs []runtimePubsub.Subscription

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		subs = runtimePubsub.DeclarativeKubernetes(a.operatorClient, a.podName, a.namespace, log)
	case modes.StandaloneMode:
		subs = runtimePubsub.DeclarativeSelfHosted(a.runtimeConfig.Standalone.ComponentsPath, log)
	}

	// only return valid subscriptions for this app id
	i := 0
	for _, s := range subs {
		keep := false
		if len(s.Scopes) == 0 {
			keep = true
		} else {
			for _, scope := range s.Scopes {
				if scope == a.runtimeConfig.ID {
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
	if a.subscriptions != nil {
		return a.subscriptions, nil
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
	if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		subscriptions, err = runtimePubsub.GetSubscriptionsHTTP(a.appChannel, log, a.resiliency)
	} else if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		var conn gogrpc.ClientConnInterface
		conn, err = a.grpc.GetAppClient()
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

	a.subscriptions = subscriptions
	return subscriptions, nil
}

func (a *DaprRuntime) getTopicRoutes() (map[string]TopicRoutes, error) {
	if a.topicRoutes != nil {
		return a.topicRoutes, nil
	}

	topicRoutes := make(map[string]TopicRoutes)

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
			topicRoutes[s.PubsubName] = TopicRoutes{}
		}

		topicRoutes[s.PubsubName][s.Topic] = TopicRouteElem{
			metadata:        s.Metadata,
			rules:           s.Rules,
			deadLetterTopic: s.DeadLetterTopic,
			bulkSubscribe:   s.BulkSubscribe,
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
	a.topicRoutes = topicRoutes
	return topicRoutes, nil
}

func (a *DaprRuntime) initPubSub(c componentsV1alpha1.Component) error {
	fName := c.LogName()
	pubSub, err := a.pubSubRegistry.Create(c.Spec.Type, c.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation", c.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}

	baseMetadata := a.toBaseMetadata(c)
	properties := baseMetadata.Properties
	consumerID := strings.TrimSpace(properties["consumerID"])
	if consumerID == "" {
		consumerID = a.runtimeConfig.ID
	}
	properties["consumerID"] = consumerID

	err = pubSub.Init(context.TODO(), pubsub.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init", c.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}

	pubsubName := c.ObjectMeta.Name

	a.pubSubs[pubsubName] = pubsubItem{
		component:           pubSub,
		scopedSubscriptions: scopes.GetScopedTopics(scopes.SubscriptionScopes, a.runtimeConfig.ID, properties),
		scopedPublishings:   scopes.GetScopedTopics(scopes.PublishingScopes, a.runtimeConfig.ID, properties),
		allowedTopics:       scopes.GetAllowedTopics(properties),
		namespaceScoped:     metadataContainsNamespace(c.Spec.Metadata),
	}
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)

	return nil
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (a *DaprRuntime) Publish(req *pubsub.PublishRequest) error {
	a.topicsLock.RLock()
	ps, ok := a.pubSubs[req.PubsubName]
	a.topicsLock.RUnlock()

	if !ok {
		return runtimePubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, ps.scopedPublishings); !allowed {
		return runtimePubsub.NotAllowedError{Topic: req.Topic, ID: a.runtimeConfig.ID}
	}

	if ps.namespaceScoped {
		req.Topic = a.namespace + req.Topic
	}

	policyRunner := resiliency.NewRunner[any](a.ctx,
		a.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, ps.component.Publish(ctx, req)
	})
	return err
}

func (a *DaprRuntime) BulkPublish(req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	// context.TODO() is used here as later on a context will have to be passed in for each publish separately
	ps, ok := a.pubSubs[req.PubsubName]
	if !ok {
		return pubsub.BulkPublishResponse{}, runtimePubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, ps.scopedPublishings); !allowed {
		return pubsub.BulkPublishResponse{}, runtimePubsub.NotAllowedError{Topic: req.Topic, ID: a.runtimeConfig.ID}
	}
	policyDef := a.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub)
	if bulkPublisher, ok := ps.component.(pubsub.BulkPublisher); ok {
		return runtimePubsub.ApplyBulkPublishResiliency(context.TODO(), req, policyDef, bulkPublisher)
	}
	log.Debugf("pubsub %s does not implement the BulkPublish API; falling back to publishing messages individually", req.PubsubName)
	defaultBulkPublisher := runtimePubsub.NewDefaultBulkPublisher(ps.component)

	return runtimePubsub.ApplyBulkPublishResiliency(context.TODO(), req, policyDef, defaultBulkPublisher)
}

func metadataContainsNamespace(items []componentsV1alpha1.MetadataItem) bool {
	for _, c := range items {
		val := c.Value.String()
		if strings.Contains(val, "{namespace}") {
			return true
		}
	}
	return false
}

// Subscribe is used by APIs to start a subscription to a topic.
func (a *DaprRuntime) Subscribe(ctx context.Context, name string, routes map[string]TopicRouteElem) (err error) {
	_, ok := a.pubSubs[name]
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
	ps, ok := a.pubSubs[pubsubName]
	if !ok {
		return nil
	}
	return ps.component
}

func (a *DaprRuntime) isPubSubOperationAllowed(pubsubName string, topic string, scopedTopics []string) bool {
	inAllowedTopics := false

	// first check if allowedTopics contain it
	if len(a.pubSubs[pubsubName].allowedTopics) > 0 {
		for _, t := range a.pubSubs[pubsubName].allowedTopics {
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

	resolverName := a.globalConfig.Spec.NameResolutionSpec.Component
	resolverVersion := a.globalConfig.Spec.NameResolutionSpec.Version

	if resolverName == "" {
		switch a.runtimeConfig.Mode {
		case modes.KubernetesMode:
			resolverName = "kubernetes"
		case modes.StandaloneMode:
			resolverName = "mdns"
		default:
			fName := utils.ComponentLogName(resolverName, "nameResolution", resolverVersion)
			return NewInitError(InitComponentFailure, fName, fmt.Errorf("unable to determine name resolver for %s mode", string(a.runtimeConfig.Mode)))
		}
	}

	if resolverVersion == "" {
		resolverVersion = components.FirstStableVersion
	}

	fName := utils.ComponentLogName(resolverName, "nameResolution", resolverVersion)
	resolver, err = a.nameResolutionRegistry.Create(resolverName, resolverVersion, fName)
	resolverMetadata.Name = resolverName
	resolverMetadata.Configuration = a.globalConfig.Spec.NameResolutionSpec.Configuration
	resolverMetadata.Properties = map[string]string{
		nr.DaprHTTPPort: strconv.Itoa(a.runtimeConfig.HTTPPort),
		nr.DaprPort:     strconv.Itoa(a.runtimeConfig.InternalGRPCPort),
		nr.AppPort:      strconv.Itoa(a.runtimeConfig.ApplicationPort),
		nr.HostAddress:  a.hostAddress,
		nr.AppID:        a.runtimeConfig.ID,
		// TODO - change other nr components to use above properties (specifically MDNS component)
		nr.MDNSInstanceName:    a.runtimeConfig.ID,
		nr.MDNSInstanceAddress: a.hostAddress,
		nr.MDNSInstancePort:    strconv.Itoa(a.runtimeConfig.InternalGRPCPort),
	}

	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "creation", resolverName)
		return NewInitError(CreateComponentFailure, fName, err)
	}

	if err = resolver.Init(resolverMetadata); err != nil {
		diag.DefaultMonitoring.ComponentInitFailed("nameResolution", "init", resolverName)
		return NewInitError(InitComponentFailure, fName, err)
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

	if cloudEvent[pubsub.TraceIDField] != nil {
		traceID := cloudEvent[pubsub.TraceIDField].(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.topic, sc, a.globalConfig.Spec.TracingSpec)
	}

	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("error from app channel while sending pub/sub event to app: %w", err)
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
		if err != nil {
			log.Debugf("skipping status check due to error parsing result from pub/sub event %v", cloudEvent[pubsub.IDField])
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
			return nil //nolint:nilerr
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
			return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
		case pubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
			return nil
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v: %v", cloudEvent[pubsub.IDField], appResponse.Status)
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
	return errors.New(errMsg)
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

				return decodeErr
			}

			envelope.Data = decoded
		} else {
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, 0)
			return ErrUnexpectedEnvelopeData
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
				return ErrUnexpectedEnvelopeData
			}
		} else if contenttype.IsJSONContentType(envelope.DataContentType) || contenttype.IsCloudEventContentType(envelope.DataContentType) {
			envelope.Data, _ = json.Marshal(data)
		}
	}

	var span trace.Span
	if iTraceID, ok := cloudEvent[pubsub.TraceIDField]; ok {
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

		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", cloudEvent[pubsub.IDField], err)
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
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
	case runtimev1pb.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)

		return nil
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	return fmt.Errorf("unknown status returned from app while processing pub/sub event %v: %v", cloudEvent[pubsub.IDField], res.GetStatus())
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
		return &extensionsStruct, fmt.Errorf("Error processing internal cloud event data: unable to marshal cloudEvent extensions: %s", jsonMarshalErr)
	}

	protoUnmarshalErr := protojson.Unmarshal(extensionBytes, &extensionsStruct)
	if protoUnmarshalErr != nil {
		return &extensionsStruct, fmt.Errorf("Error processing internal cloud event data: unable to unmarshal cloudEvent extensions to proto struct: %s", protoUnmarshalErr)
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
	err := actors.ValidateHostEnvironment(a.runtimeConfig.mtlsEnabled, a.runtimeConfig.Mode, a.namespace)
	if err != nil {
		return NewInitError(InitFailure, "actors", err)
	}
	a.actorStateStoreLock.Lock()
	defer a.actorStateStoreLock.Unlock()
	if a.actorStateStoreName == "" {
		log.Info("actors: state store is not configured - this is okay for clients but services with hosted actors will fail to initialize!")
	}
	actorConfig := actors.NewConfig(actors.ConfigOpts{
		HostAddress:        a.hostAddress,
		AppID:              a.runtimeConfig.ID,
		PlacementAddresses: a.runtimeConfig.PlacementAddresses,
		Port:               a.runtimeConfig.InternalGRPCPort,
		Namespace:          a.namespace,
		AppConfig:          a.appConfig,
	})

	act := actors.NewActors(actors.ActorsOpts{
		StateStore:       a.stateStores[a.actorStateStoreName],
		AppChannel:       a.appChannel,
		GRPCConnectionFn: a.grpc.GetGRPCConnection,
		Config:           actorConfig,
		CertChain:        a.runtimeConfig.CertChain,
		TracingSpec:      a.globalConfig.Spec.TracingSpec,
		Resiliency:       a.resiliency,
		StateStoreName:   a.actorStateStoreName,
	})
	err = act.Init()
	if err == nil {
		a.actor = act
		return nil
	}
	return NewInitError(InitFailure, "actors", err)
}

func (a *DaprRuntime) getAuthorizedComponents(components []componentsV1alpha1.Component) []componentsV1alpha1.Component {
	authorized := make([]componentsV1alpha1.Component, len(components))

	i := 0
	for _, c := range components {
		if a.isComponentAuthorized(c) {
			authorized[i] = c
			i++
		}
	}
	return authorized[0:i]
}

func (a *DaprRuntime) isComponentAuthorized(component componentsV1alpha1.Component) bool {
	for _, auth := range a.componentAuthorizers {
		if !auth(component) {
			return false
		}
	}
	return true
}

func (a *DaprRuntime) namespaceComponentAuthorizer(component componentsV1alpha1.Component) bool {
	if a.namespace == "" || (a.namespace != "" && component.ObjectMeta.Namespace == a.namespace) {
		if len(component.Scopes) == 0 {
			return true
		}

		// scopes are defined, make sure this runtime ID is authorized
		for _, s := range component.Scopes {
			if s == a.runtimeConfig.ID {
				return true
			}
		}
	}

	return false
}

func (a *DaprRuntime) loadComponents(opts *runtimeOpts) error {
	var loader components.ComponentLoader

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		loader = components.NewKubernetesComponents(a.runtimeConfig.Kubernetes, a.namespace, a.operatorClient, a.podName)
	case modes.StandaloneMode:
		loader = components.NewStandaloneComponents(a.runtimeConfig.Standalone)
	default:
		return fmt.Errorf("components loader for mode %s not found", a.runtimeConfig.Mode)
	}

	log.Info("loading components")
	comps, err := loader.LoadComponents()
	if err != nil {
		return err
	}

	for _, comp := range comps {
		log.Debugf("found component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
	}

	authorizedComps := a.getAuthorizedComponents(comps)

	a.componentsLock.Lock()
	a.components = authorizedComps
	a.componentsLock.Unlock()

	// Iterate through the list twice
	// First, we look for secret stores and load those, then all other components
	// Sure, we could sort the list of authorizedComps... but this is simpler and most certainly faster
	for _, comp := range authorizedComps {
		if strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			a.pendingComponents <- comp
		}
	}
	for _, comp := range authorizedComps {
		if !strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			a.pendingComponents <- comp
		}
	}

	return nil
}

func (a *DaprRuntime) appendOrReplaceComponents(component componentsV1alpha1.Component) {
	a.componentsLock.Lock()
	defer a.componentsLock.Unlock()

	replaced := false
	for i, c := range a.components {
		if c.Spec.Type == component.Spec.Type && c.ObjectMeta.Name == component.Name {
			a.components[i] = component
			replaced = true
			break
		}
	}

	if !replaced {
		a.components = append(a.components, component)
	}
}

func (a *DaprRuntime) extractComponentCategory(component componentsV1alpha1.Component) components.Category {
	for _, category := range componentCategoriesNeedProcess {
		if strings.HasPrefix(component.Spec.Type, string(category)+".") {
			return category
		}
	}
	return ""
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
				log.Warnf("error processing component, daprd process will exit gracefully")
				a.Shutdown(a.runtimeConfig.GracefulShutdownDuration)
				log.Fatalf(e)
			}
			log.Errorf(e)
		}
	}
}

func (a *DaprRuntime) flushOutstandingComponents() {
	log.Info("waiting for all outstanding components to be processed")
	// We flush by sending a no-op component. Since the processComponents goroutine only reads one component at a time,
	// We know that once the no-op component is read from the channel, all previous components will have been fully processed.
	a.pendingComponents <- componentsV1alpha1.Component{}
	log.Info("all outstanding components processed")
}

func (a *DaprRuntime) processComponentAndDependents(comp componentsV1alpha1.Component) error {
	log.Debugf("loading component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
	res := a.preprocessOneComponent(&comp)
	if res.unreadyDependency != "" {
		a.pendingComponentDependents[res.unreadyDependency] = append(a.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := a.extractComponentCategory(comp)
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
		ch <- a.doProcessOneComponent(compCategory, comp)
	}()

	select {
	case err := <-ch:
		if err != nil {
			return err
		}
	case <-time.After(timeout):
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		err := fmt.Errorf("init timeout for component %s exceeded after %s", comp.Name, timeout.String())
		return NewInitError(InitComponentFailure, comp.LogName(), err)
	}

	log.Infof("component loaded. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
	a.appendOrReplaceComponents(comp)
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

func (a *DaprRuntime) doProcessOneComponent(category components.Category, comp componentsV1alpha1.Component) error {
	switch category {
	case components.CategoryBindings:
		return a.initBinding(comp)
	case components.CategoryPubSub:
		return a.initPubSub(comp)
	case components.CategorySecretStore:
		return a.initSecretStore(comp)
	case components.CategoryCryptoProvider:
		return a.initCryptoProvider(comp)
	case components.CategoryStateStore:
		return a.initState(comp)
	case components.CategoryConfiguration:
		return a.initConfiguration(comp)
	case components.CategoryLock:
		return a.initLock(comp)
	case components.CategoryWorkflow:
		return a.initWorkflowComponent(comp)
	}
	return nil
}

func (a *DaprRuntime) preprocessOneComponent(comp *componentsV1alpha1.Component) componentPreprocessRes {
	var unreadySecretsStore string
	*comp, unreadySecretsStore = a.processComponentSecrets(*comp)
	if unreadySecretsStore != "" {
		return componentPreprocessRes{
			unreadyDependency: componentDependency(components.CategorySecretStore, unreadySecretsStore),
		}
	}
	return componentPreprocessRes{}
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
	for name, component := range a.secretStores {
		closeComponent(component, "secret store "+name, &merr)
	}
	for name, component := range a.stateStores {
		closeComponent(component, "state store "+name, &merr)
	}
	for name, component := range a.lockStores {
		closeComponent(component, "lock store "+name, &merr)
	}
	for name, component := range a.configurationStores {
		closeComponent(component, "configuration store "+name, &merr)
	}
	for name, component := range a.cryptoProviders {
		closeComponent(component, "crypto provider "+name, &merr)
	}
	for name, component := range a.workflowComponents {
		closeComponent(component, "workflow "+name, &merr)
	}
	// Close output bindings
	// Input bindings are closed when a.ctx is canceled
	for name, component := range a.outputBindings {
		closeComponent(component, "output binding "+name, &merr)
	}
	// Close pubsub publisher
	// The subscriber part is closed when a.ctx is canceled
	for name, pubSub := range a.pubSubs {
		if pubSub.component == nil {
			continue
		}
		closeComponent(pubSub.component, "pub sub "+name, &merr)
	}
	closeComponent(a.nameResolver, "name resolver", &merr)

	return merr
}

func closeComponent(component any, logmsg string, merr *error) {
	if closer, ok := component.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			err = fmt.Errorf("error closing %s: %w", logmsg, err)
			*merr = multierror.Append(*merr, err)
			log.Warn(err)
		}
	}
}

// ShutdownWithWait will gracefully stop runtime and wait outstanding operations.
func (a *DaprRuntime) ShutdownWithWait() {
	// Another shutdown signal causes an instant shutdown and interrupts the graceful shutdown
	go func() {
		<-ShutdownSignal()
		log.Info("Received another shutdown signal - shutting down right away")
		os.Exit(0)
	}()

	a.Shutdown(a.runtimeConfig.GracefulShutdownDuration)
	os.Exit(0)
}

func (a *DaprRuntime) cleanSocket() {
	if a.runtimeConfig.UnixDomainSocket != "" {
		for _, s := range []string{"http", "grpc"} {
			os.Remove(fmt.Sprintf("%s/dapr-%s-%s.socket", a.runtimeConfig.UnixDomainSocket, a.runtimeConfig.ID, s))
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

	log.Infof("Holding shutdown for %s to allow graceful stop of outstanding operations", duration.String())
	<-time.After(duration)

	log.Info("Stopping Dapr APIs")
	for _, closer := range a.apiClosers {
		if err := closer.Close(); err != nil {
			log.Warnf("error closing API: %v", err)
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

// Returns the component updated with the secrets applied.
// If the component references a secret store that hasn't been loaded yet, it returns the name of the secret store component as second returned value.
func (a *DaprRuntime) processComponentSecrets(component componentsV1alpha1.Component) (componentsV1alpha1.Component, string) {
	cache := map[string]secretstores.GetSecretResponse{}

	for i, m := range component.Spec.Metadata {
		if m.SecretKeyRef.Name == "" {
			continue
		}

		secretStoreName := a.authSecretStoreOrDefault(component)
		secretStore := a.getSecretStore(secretStoreName)
		if secretStore == nil {
			log.Warnf("component %s references a secret store that isn't loaded: %s", component.Name, secretStoreName)
			return component, secretStoreName
		}

		// If running in Kubernetes, do not fetch secrets from the Kubernetes secret store as they will be populated by the operator.
		// Instead, base64 decode the secret values into their real self.
		if a.runtimeConfig.Mode == modes.KubernetesMode && secretStoreName == secretstoresLoader.BuiltinKubernetesSecretStore {
			var jsonVal string
			err := json.Unmarshal(m.Value.Raw, &jsonVal)
			if err != nil {
				log.Errorf("error decoding secret: %s", err)
				continue
			}

			dec, err := base64.StdEncoding.DecodeString(jsonVal)
			if err != nil {
				log.Errorf("error decoding secret: %s", err)
				continue
			}

			m.Value = componentsV1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: dec,
				},
			}

			component.Spec.Metadata[i] = m
			continue
		}

		resp, ok := cache[m.SecretKeyRef.Name]
		if !ok {
			// TODO: cascade context.
			r, err := secretStore.GetSecret(context.TODO(), secretstores.GetSecretRequest{
				Name: m.SecretKeyRef.Name,
				Metadata: map[string]string{
					"namespace": component.ObjectMeta.Namespace,
				},
			})
			if err != nil {
				log.Errorf("error getting secret: %s", err)
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
		if ok {
			component.Spec.Metadata[i].Value = componentsV1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(val),
				},
			}
		}

		cache[m.SecretKeyRef.Name] = resp
	}
	return component, ""
}

func (a *DaprRuntime) authSecretStoreOrDefault(comp componentsV1alpha1.Component) string {
	if comp.SecretStore == "" {
		switch a.runtimeConfig.Mode {
		case modes.KubernetesMode:
			return "kubernetes"
		}
	}
	return comp.SecretStore
}

func (a *DaprRuntime) getSecretStore(storeName string) secretstores.SecretStore {
	if storeName == "" {
		return nil
	}
	return a.secretStores[storeName]
}

func (a *DaprRuntime) blockUntilAppIsReady() {
	if a.runtimeConfig.ApplicationPort <= 0 {
		return
	}

	log.Infof("application protocol: %s. waiting on port %v.  This will block until the app is listening on that port.", string(a.runtimeConfig.ApplicationProtocol), a.runtimeConfig.ApplicationPort)

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
		conn, _ := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(a.runtimeConfig.ApplicationPort), time.Millisecond*500)
		if conn != nil {
			conn.Close()
			break
		}
		// prevents overwhelming the OS with open connections
		time.Sleep(time.Millisecond * 50)
	}

	log.Infof("application discovered on port %v", a.runtimeConfig.ApplicationPort)
}

func (a *DaprRuntime) loadAppConfiguration() {
	if a.appChannel == nil {
		return
	}

	appConfig, err := a.appChannel.GetAppConfig()
	if err != nil {
		return
	}

	if appConfig != nil {
		a.appConfig = *appConfig
		log.Info("application configuration loaded")
	}
}

func (a *DaprRuntime) createAppChannel() (err error) {
	if a.runtimeConfig.ApplicationPort == 0 {
		log.Warn("App channel is not initialized. Did you configure an app-port?")
		return nil
	}

	var ch channel.AppChannel
	switch a.runtimeConfig.ApplicationProtocol {
	case GRPCProtocol:
		ch, err = a.grpc.GetAppChannel()
		if err != nil {
			return err
		}
	case HTTPProtocol:
		pipeline, err := a.buildAppHTTPPipeline()
		if err != nil {
			return err
		}
		ch, err = httpChannel.CreateLocalChannel(a.runtimeConfig.ApplicationPort, a.runtimeConfig.MaxConcurrency, pipeline, a.globalConfig.Spec.TracingSpec, a.runtimeConfig.AppSSL, a.runtimeConfig.MaxRequestBodySize, a.runtimeConfig.ReadBufferSize)
		if err != nil {
			return err
		}
		ch.(*httpChannel.Channel).SetAppHealthCheckPath(a.runtimeConfig.AppHealthCheckHTTPPath)
	default:
		return fmt.Errorf("cannot create app channel for protocol %s", a.runtimeConfig.ApplicationProtocol)
	}

	a.appChannel = ch

	return nil
}

func (a *DaprRuntime) appendBuiltinSecretStore() {
	if a.runtimeConfig.DisableBuiltinK8sSecretStore {
		return
	}

	switch a.runtimeConfig.Mode {
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

func (a *DaprRuntime) initCryptoProvider(c componentsV1alpha1.Component) error {
	fName := c.LogName()
	component, err := a.cryptoProviderRegistry.Create(c.Spec.Type, c.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation", c.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}

	err = component.Init(context.TODO(), contribCrypto.Metadata{Base: a.toBaseMetadata(c)})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init", c.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}

	a.cryptoProviders[c.ObjectMeta.Name] = component
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) initSecretStore(c componentsV1alpha1.Component) error {
	fName := c.LogName()
	secretStore, err := a.secretStoresRegistry.Create(c.Spec.Type, c.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation", c.ObjectMeta.Name)
		return NewInitError(CreateComponentFailure, fName, err)
	}

	err = secretStore.Init(context.TODO(), secretstores.Metadata{Base: a.toBaseMetadata(c)})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init", c.ObjectMeta.Name)
		return NewInitError(InitComponentFailure, fName, err)
	}

	a.secretStores[c.ObjectMeta.Name] = secretStore
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) convertMetadataItemsToProperties(items []componentsV1alpha1.MetadataItem) map[string]string {
	properties := map[string]string{}
	for _, c := range items {
		val := c.Value.String()
		for strings.Contains(val, "{uuid}") {
			val = strings.Replace(val, "{uuid}", uuid.New().String(), 1)
		}
		for strings.Contains(val, "{podName}") {
			if a.podName == "" {
				log.Fatalf("failed to parse metadata: property %s refers to {podName} but podName is not set", c.Name)
			}
			val = strings.Replace(val, "{podName}", a.podName, 1)
		}
		for strings.Contains(val, "{namespace}") {
			val = strings.Replace(val, "{namespace}", fmt.Sprintf("%s.%s", a.namespace, a.runtimeConfig.ID), 1)
		}
		for strings.Contains(val, "{appID}") {
			val = strings.Replace(val, "{appID}", a.runtimeConfig.ID, 1)
		}
		properties[c.Name] = val
	}
	return properties
}

func (a *DaprRuntime) toBaseMetadata(c componentsV1alpha1.Component) contribMetadata.Base {
	return contribMetadata.Base{
		Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
		Name:       c.Name,
	}
}

func (a *DaprRuntime) getComponent(componentType string, name string) (componentsV1alpha1.Component, bool) {
	a.componentsLock.RLock()
	defer a.componentsLock.RUnlock()

	for i, c := range a.components {
		if c.Spec.Type == componentType && c.ObjectMeta.Name == name {
			return a.components[i], true
		}
	}
	return componentsV1alpha1.Component{}, false
}

func (a *DaprRuntime) getComponents() []componentsV1alpha1.Component {
	a.componentsLock.RLock()
	defer a.componentsLock.RUnlock()

	comps := make([]componentsV1alpha1.Component, len(a.components))
	copy(comps, a.components)
	return comps
}

func (a *DaprRuntime) getComponentsCapabilitesMap() map[string][]string {
	capabilities := make(map[string][]string)
	for key, store := range a.stateStores {
		features := store.Features()
		stateStoreCapabilities := featureTypeToString(features)
		if state.FeatureETag.IsPresent(features) && state.FeatureTransactional.IsPresent(features) {
			stateStoreCapabilities = append(stateStoreCapabilities, "ACTOR")
		}
		capabilities[key] = stateStoreCapabilities
	}
	for key, pubSubItem := range a.pubSubs {
		features := pubSubItem.component.Features()
		capabilities[key] = featureTypeToString(features)
	}
	for key := range a.inputBindings {
		capabilities[key] = []string{"INPUT_BINDING"}
	}
	for key := range a.outputBindings {
		if val, found := capabilities[key]; found {
			capabilities[key] = append(val, "OUTPUT_BINDING")
		} else {
			capabilities[key] = []string{"OUTPUT_BINDING"}
		}
	}
	for key, store := range a.secretStores {
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
	if !a.runtimeConfig.mtlsEnabled {
		log.Info("mTLS is disabled. Skipping certificate request and tls validation")
		return nil
	}
	if sentryAddress == "" {
		return errors.New("sentryAddress cannot be empty")
	}
	log.Info("mTLS enabled. creating sidecar authenticator")

	auth, err := security.GetSidecarAuthenticator(sentryAddress, a.runtimeConfig.CertChain)
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
	for pubsubName := range a.pubSubs {
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
	a.topicRoutes = nil
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

	for name, binding := range a.inputBindings {
		isSubscribed, err := a.isAppSubscribedToBinding(name)
		if err != nil {
			return err
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

func createGRPCManager(runtimeConfig *Config, globalConfig *config.Configuration) *grpc.Manager {
	grpcAppChannelConfig := &grpc.AppChannelConfig{}
	if globalConfig != nil {
		grpcAppChannelConfig.TracingSpec = globalConfig.Spec.TracingSpec
	}
	if runtimeConfig != nil {
		grpcAppChannelConfig.Port = runtimeConfig.ApplicationPort
		grpcAppChannelConfig.MaxConcurrency = runtimeConfig.MaxConcurrency
		grpcAppChannelConfig.SSLEnabled = runtimeConfig.AppSSL
		grpcAppChannelConfig.MaxRequestBodySizeMB = runtimeConfig.MaxRequestBodySize
		grpcAppChannelConfig.ReadBufferSizeKB = runtimeConfig.ReadBufferSize
	}

	m := grpc.NewGRPCManager(runtimeConfig.Mode, grpcAppChannelConfig)
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
