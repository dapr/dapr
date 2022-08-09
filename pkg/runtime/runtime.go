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
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dapr/components-contrib/lock"
	lock_loader "github.com/dapr/dapr/pkg/components/lock"

	"github.com/cenkalti/backoff/v4"

	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/contenttype"
	contrib_metadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/middleware"
	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	configuration_loader "github.com/dapr/dapr/pkg/components/configuration"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	http_channel "github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/components"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	nr_loader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/utils"
)

const (
	actorStateStore = "actorStateStore"

	// output bindings concurrency.
	bindingsConcurrencyParallel   = "parallel"
	bindingsConcurrencySequential = "sequential"
	pubsubName                    = "pubsubName"
	deadLetterKeyFormat           = "%s||%s" // componentName||topicName

	// hot reloading is currently unsupported, but
	// setting this environment variable restores the
	// partial hot reloading support for k8s.
	hotReloadingEnvVar = "DAPR_ENABLE_HOT_RELOADING"
)

type ComponentCategory string

const (
	bindingsComponent               ComponentCategory = "bindings"
	pubsubComponent                 ComponentCategory = "pubsub"
	secretStoreComponent            ComponentCategory = "secretstores"
	stateComponent                  ComponentCategory = "state"
	middlewareComponent             ComponentCategory = "middleware"
	configurationComponent          ComponentCategory = "configuration"
	lockComponent                   ComponentCategory = "lock"
	defaultComponentInitTimeout                       = time.Second * 5
	defaultGracefulShutdownDuration                   = time.Second * 5
)

var componentCategoriesNeedProcess = []ComponentCategory{
	bindingsComponent,
	pubsubComponent,
	secretStoreComponent,
	stateComponent,
	middlewareComponent,
	configurationComponent,
	lockComponent,
}

var log = logger.NewLogger("dapr.runtime")

// ErrUnexpectedEnvelopeData denotes that an unexpected data type
// was encountered when processing a cloud event's data property.
var ErrUnexpectedEnvelopeData = errors.New("unexpected data type encountered in envelope")

type Route struct {
	metadata map[string]string
	rules    []*runtime_pubsub.Rule
}

type TopicRoute struct {
	routes map[string]Route
}

// Type of function that determines if a component is authorized.
// The function receives the component and must return true if the component is authorized.
type ComponentAuthorizer func(component components_v1alpha1.Component) bool

// DaprRuntime holds all the core components of the runtime.
type DaprRuntime struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
	runtimeConfig          *Config
	globalConfig           *config.Configuration
	accessControlList      *config.AccessControlList
	componentsLock         *sync.RWMutex
	components             []components_v1alpha1.Component
	grpc                   *grpc.Manager
	appChannel             channel.AppChannel
	appConfig              config.ApplicationConfig
	directMessaging        messaging.DirectMessaging
	stateStoreRegistry     state_loader.Registry
	secretStoresRegistry   secretstores_loader.Registry
	nameResolutionRegistry nr_loader.Registry
	stateStores            map[string]state.Store
	actor                  actors.Actors
	bindingsRegistry       bindings_loader.Registry
	subscribeBindingList   []string
	inputBindings          map[string]bindings.InputBinding
	outputBindings         map[string]bindings.OutputBinding
	secretStores           map[string]secretstores.SecretStore
	pubSubRegistry         pubsub_loader.Registry
	pubSubs                map[string]pubsub.PubSub
	nameResolver           nr.Resolver
	httpMiddlewareRegistry http_middleware_loader.Registry
	hostAddress            string
	actorStateStoreName    string
	actorStateStoreLock    *sync.RWMutex
	authenticator          security.Authenticator
	namespace              string
	podName                string
	scopedSubscriptions    map[string][]string
	scopedPublishings      map[string][]string
	allowedTopics          map[string][]string
	daprHTTPAPI            http.API
	operatorClient         operatorv1pb.OperatorClient
	topicRoutes            map[string]TopicRoute
	deadLetterTopics       map[string]string
	inputBindingRoutes     map[string]string
	shutdownC              chan error
	apiClosers             []io.Closer
	componentAuthorizers   []ComponentAuthorizer

	secretsConfiguration map[string]config.SecretsScope

	configurationStoreRegistry configuration_loader.Registry
	configurationStores        map[string]configuration.Store

	lockStoreRegistry lock_loader.Registry
	lockStores        map[string]lock.Store

	pendingComponents          chan components_v1alpha1.Component
	pendingComponentDependents map[string][]components_v1alpha1.Component

	proxy messaging.Proxy

	resiliency resiliency.Provider

	tracerProvider *sdktrace.TracerProvider
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

// NewDaprRuntime returns a new runtime with the given runtime config and global config.
func NewDaprRuntime(runtimeConfig *Config, globalConfig *config.Configuration, accessControlList *config.AccessControlList, resiliencyProvider resiliency.Provider) *DaprRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &DaprRuntime{
		ctx:                    ctx,
		cancel:                 cancel,
		runtimeConfig:          runtimeConfig,
		globalConfig:           globalConfig,
		accessControlList:      accessControlList,
		componentsLock:         &sync.RWMutex{},
		components:             make([]components_v1alpha1.Component, 0),
		actorStateStoreLock:    &sync.RWMutex{},
		grpc:                   grpc.NewGRPCManager(runtimeConfig.Mode),
		inputBindings:          map[string]bindings.InputBinding{},
		outputBindings:         map[string]bindings.OutputBinding{},
		secretStores:           map[string]secretstores.SecretStore{},
		stateStores:            map[string]state.Store{},
		pubSubs:                map[string]pubsub.PubSub{},
		stateStoreRegistry:     state_loader.NewRegistry(),
		bindingsRegistry:       bindings_loader.NewRegistry(),
		pubSubRegistry:         pubsub_loader.NewRegistry(),
		secretStoresRegistry:   secretstores_loader.NewRegistry(),
		nameResolutionRegistry: nr_loader.NewRegistry(),
		httpMiddlewareRegistry: http_middleware_loader.NewRegistry(),

		scopedSubscriptions: map[string][]string{},
		scopedPublishings:   map[string][]string{},
		allowedTopics:       map[string][]string{},
		inputBindingRoutes:  map[string]string{},

		secretsConfiguration:       map[string]config.SecretsScope{},
		configurationStoreRegistry: configuration_loader.NewRegistry(),
		configurationStores:        map[string]configuration.Store{},

		lockStoreRegistry: lock_loader.NewRegistry(),
		lockStores:        map[string]lock.Store{},

		pendingComponents:          make(chan components_v1alpha1.Component),
		pendingComponentDependents: map[string][]components_v1alpha1.Component{},
		shutdownC:                  make(chan error, 1),

		tracerProvider: nil,

		resiliency: resiliencyProvider,
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

	err := a.initRuntime(&o)
	if err != nil {
		return err
	}

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
			return nil, errors.Wrap(err, "error creating operator client")
		}
		return client, nil
	}
	return nil, nil
}

// setupTracing set up the trace exporters. Technically we don't need to pass `hostAddress` in,
// but we do so here to explicitly call out the dependency on having `hostAddress` computed.
func (a *DaprRuntime) setupTracing(hostAddress string, tpStore tracerProviderStore) error {
	// Register stdout trace exporter if user wants to debug requests or log as Info level.
	if a.globalConfig.Spec.TracingSpec.Stdout {
		tpStore.RegisterExporter(diag_utils.NewStdOutExporter())
	}

	// Register zipkin trace exporter if ZipkinSpec is specified
	if a.globalConfig.Spec.TracingSpec.Zipkin.EndpointAddress != "" {
		zipkinExporter, err := zipkin.New(a.globalConfig.Spec.TracingSpec.Zipkin.EndpointAddress)
		if err != nil {
			return err
		}
		tpStore.RegisterExporter(zipkinExporter)
	}

	// Register otel trace exporter if OtelSpec is specified
	if a.globalConfig.Spec.TracingSpec.Otel.EndpointAddress != "" && a.globalConfig.Spec.TracingSpec.Otel.Protocol != "" {
		endpoint := a.globalConfig.Spec.TracingSpec.Otel.EndpointAddress
		protocol := a.globalConfig.Spec.TracingSpec.Otel.Protocol
		if protocol != "http" && protocol != "grpc" {
			return fmt.Errorf("invalid protocol %v provided for Otel endpoint", protocol)
		}
		isSecure := a.globalConfig.Spec.TracingSpec.Otel.IsSecure

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
		otelExporter, err := otlptrace.New(context.Background(), client)
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
	tpStore.RegisterSampler(diag_utils.TraceSampler(a.globalConfig.Spec.TracingSpec.SamplingRate))

	tpStore.RegisterTracerProvider()

	return nil
}

func (a *DaprRuntime) initRuntime(opts *runtimeOpts) error {
	a.namespace = a.getNamespace()

	// Initialize metrics only if MetricSpec is enabled.
	if a.globalConfig.Spec.MetricSpec.Enabled {
		if err := diag.InitMetrics(a.runtimeConfig.ID, a.namespace); err != nil {
			log.Errorf("failed to initialize metrics: %v", err)
		}
	}

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
		return errors.Wrap(err, "failed to determine host address")
	}
	if err = a.setupTracing(a.hostAddress, newOpentelemetryTracerProviderStore()); err != nil {
		return errors.Wrap(err, "failed to setup tracing")
	}
	// Register and initialize name resolution for service discovery.
	a.nameResolutionRegistry.Register(opts.nameResolutions...)
	err = a.initNameResolution()
	if err != nil {
		log.Warnf("failed to init name resolution: %s", err)
	}

	a.pubSubRegistry.Register(opts.pubsubs...)
	a.secretStoresRegistry.Register(opts.secretStores...)
	a.stateStoreRegistry.Register(opts.states...)
	a.configurationStoreRegistry.Register(opts.configurations...)
	a.bindingsRegistry.RegisterInputBindings(opts.inputBindings...)
	a.bindingsRegistry.RegisterOutputBindings(opts.outputBindings...)
	a.httpMiddlewareRegistry.Register(opts.httpMiddleware...)
	a.lockStoreRegistry.Register(opts.locks...)

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
	grpcAPI := a.getGRPCAPI()

	err = a.startGRPCAPIServer(grpcAPI, a.runtimeConfig.APIGRPCPort)
	if err != nil {
		log.Fatalf("failed to start API gRPC server: %s", err)
	}
	if a.runtimeConfig.UnixDomainSocket != "" {
		log.Info("API gRPC server is running on a unix domain socket")
	} else {
		log.Infof("API gRPC server is running on port %v", a.runtimeConfig.APIGRPCPort)
	}

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

	err = a.startGRPCInternalServer(grpcAPI, a.runtimeConfig.InternalGRPCPort)
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
	grpcAPI.SetAppChannel(a.appChannel)

	a.loadAppConfiguration()

	a.initDirectMessaging(a.nameResolver)

	a.daprHTTPAPI.SetDirectMessaging(a.directMessaging)
	grpcAPI.SetDirectMessaging(a.directMessaging)

	if len(a.runtimeConfig.PlacementAddresses) != 0 {
		err = a.initActors()
		if err != nil {
			log.Warnf("failed to init actors: %v", err)
		} else {
			a.daprHTTPAPI.SetActorRuntime(a.actor)
			grpcAPI.SetActorRuntime(a.actor)
		}
	}

	if opts.componentsCallback != nil {
		if err = opts.componentsCallback(ComponentRegistry{
			Actors:          a.actor,
			DirectMessaging: a.directMessaging,
			StateStores:     a.stateStores,
			InputBindings:   a.inputBindings,
			OutputBindings:  a.outputBindings,
			SecretStores:    a.secretStores,
			PubSubs:         a.pubSubs,
		}); err != nil {
			log.Fatalf("failed to register components with callback: %s", err)
		}
	}

	a.startSubscribing()
	err = a.startReadingFromBindings()
	if err != nil {
		log.Warnf("failed to read from bindings: %s ", err)
	}
	return nil
}

func (a *DaprRuntime) populateSecretsConfiguration() {
	// Populate in a map for easy lookup by store name.
	for _, scope := range a.globalConfig.Spec.Secrets.Scopes {
		a.secretsConfiguration[scope.StoreName] = scope
	}
}

func (a *DaprRuntime) buildHTTPPipeline() (http_middleware.Pipeline, error) {
	var handlers []http_middleware.Middleware

	if a.globalConfig != nil {
		for i := 0; i < len(a.globalConfig.Spec.HTTPPipelineSpec.Handlers); i++ {
			middlewareSpec := a.globalConfig.Spec.HTTPPipelineSpec.Handlers[i]
			component, exists := a.getComponent(middlewareSpec.Type, middlewareSpec.Name)
			if !exists {
				return http_middleware.Pipeline{}, errors.Errorf("couldn't find middleware component with name %s and type %s/%s",
					middlewareSpec.Name,
					middlewareSpec.Type,
					middlewareSpec.Version)
			}
			handler, err := a.httpMiddlewareRegistry.Create(middlewareSpec.Type, middlewareSpec.Version,
				middleware.Metadata{Properties: a.convertMetadataItemsToProperties(component.Spec.Metadata)})
			if err != nil {
				return http_middleware.Pipeline{}, err
			}
			log.Infof("enabled %s/%s http middleware", middlewareSpec.Type, middlewareSpec.Version)
			handlers = append(handlers, handler)
		}
	}
	return http_middleware.Pipeline{Handlers: handlers}, nil
}

func (a *DaprRuntime) initBinding(c components_v1alpha1.Component) error {
	if a.bindingsRegistry.HasOutputBinding(c.Spec.Type, c.Spec.Version) {
		if err := a.initOutputBinding(c); err != nil {
			log.Errorf("failed to init output bindings: %s", err)
			return err
		}
	}

	if a.bindingsRegistry.HasInputBinding(c.Spec.Type, c.Spec.Version) {
		if err := a.initInputBinding(c); err != nil {
			log.Errorf("failed to init input bindings: %s", err)
			return err
		}
	}
	return nil
}

func (a *DaprRuntime) sendToDeadLetterIfConfigured(name string, msg *pubsub.NewMessage) (isDeadLetterConfigured bool, err error) {
	deadLetterTopic, ok := a.deadLetterTopics[fmt.Sprintf(deadLetterKeyFormat, name, msg.Topic)]
	if !ok {
		return false, nil
	}
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
	}
	return true, err
}

func (a *DaprRuntime) beginPubSub(subscribeCtx context.Context, name string, ps pubsub.PubSub) error {
	var publishFunc func(ctx context.Context, msg *pubsubSubscribedMessage) error
	switch a.runtimeConfig.ApplicationProtocol {
	case HTTPProtocol:
		publishFunc = a.publishMessageHTTP
	case GRPCProtocol:
		publishFunc = a.publishMessageGRPC
	}
	topicRoutes, err := a.getTopicRoutes()
	if err != nil {
		return err
	}
	v, ok := topicRoutes[name]
	if !ok {
		return nil
	}
	for topic, route := range v.routes {
		allowed := a.isPubSubOperationAllowed(name, topic, a.scopedSubscriptions[name])
		if !allowed {
			log.Warnf("subscription to topic %s on pubsub %s is not allowed", topic, name)
			continue
		}

		log.Debugf("subscribing to topic=%s on pubsub=%s", topic, name)

		routeMetadata := route.metadata
		routeRules := route.rules
		if err := ps.Subscribe(subscribeCtx, pubsub.SubscribeRequest{
			Topic:    topic,
			Metadata: route.metadata,
		}, func(ctx context.Context, msg *pubsub.NewMessage) error {
			if msg.Metadata == nil {
				msg.Metadata = make(map[string]string, 1)
			}

			msg.Metadata[pubsubName] = name

			rawPayload, err := contrib_metadata.IsRawPayload(routeMetadata)
			if err != nil {
				log.Errorf("error deserializing pubsub metadata: %s", err)
				if configured, dlqErr := a.sendToDeadLetterIfConfigured(name, msg); configured && dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
					return nil
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
				return err
			}

			var cloudEvent map[string]interface{}
			data := msg.Data
			if rawPayload {
				cloudEvent = pubsub.FromRawPayload(msg.Data, msg.Topic, name)
				data, err = json.Marshal(cloudEvent)
				if err != nil {
					log.Errorf("error serializing cloud event in pubsub %s and topic %s: %s", name, msg.Topic, err)
					if configured, dlqErr := a.sendToDeadLetterIfConfigured(name, msg); configured && dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
						return nil
					}
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
					return err
				}
			} else {
				err = json.Unmarshal(msg.Data, &cloudEvent)
				if err != nil {
					log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msg.Topic, err)
					if configured, dlqErr := a.sendToDeadLetterIfConfigured(name, msg); configured && dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
						return nil
					}
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
					return err
				}
			}

			if pubsub.HasExpired(cloudEvent) {
				log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.ExpirationField])
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)

				a.sendToDeadLetterIfConfigured(name, msg)
				return nil
			}

			routePath, shouldProcess, err := findMatchingRoute(routeRules, cloudEvent)
			if err != nil {
				log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[pubsub.IDField], name, msg.Topic, err)
				if configured, dlqErr := a.sendToDeadLetterIfConfigured(name, msg); configured && dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
					return nil
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
				return err
			}
			if !shouldProcess {
				// The event does not match any route specified so ignore it.
				log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[pubsub.IDField], name, msg.Topic)
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
				a.sendToDeadLetterIfConfigured(name, msg)
				return nil
			}

			policy := a.resiliency.ComponentInboundPolicy(ctx, name)
			err = policy(func(ctx context.Context) error {
				return publishFunc(ctx, &pubsubSubscribedMessage{
					cloudEvent: cloudEvent,
					data:       data,
					topic:      msg.Topic,
					metadata:   msg.Metadata,
					path:       routePath,
					pubsub:     name,
				})
			})
			if err != nil && err != context.Canceled {
				// Sending msg to dead letter queue, if no DLQ is configured, return error for backwards compatibility(component level retry).
				if configured, _ := a.sendToDeadLetterIfConfigured(name, msg); !configured {
					return err
				}

				return nil
			}
			return err
		}); err != nil {
			log.Errorf("failed to subscribe to topic %s: %s", topic, err)
		}
	}

	return nil
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*runtime_pubsub.Rule, cloudEvent interface{}) (path string, shouldProcess bool, err error) {
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

func matchRoutingRule(rules []*runtime_pubsub.Rule, data map[string]interface{}) (*runtime_pubsub.Rule, error) {
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
			return nil, errors.Errorf("the result of match expression %s was not a boolean", rule.Match)
		}

		if result {
			return rule, nil
		}
	}

	return nil, nil
}

func (a *DaprRuntime) initDirectMessaging(resolver nr.Resolver) {
	a.directMessaging = messaging.NewDirectMessaging(
		a.runtimeConfig.ID,
		a.namespace,
		a.runtimeConfig.InternalGRPCPort,
		a.runtimeConfig.Mode,
		a.appChannel,
		a.grpc.GetGRPCConnection,
		resolver,
		a.globalConfig.Spec.TracingSpec,
		a.runtimeConfig.MaxRequestBodySize,
		a.proxy,
		a.runtimeConfig.ReadBufferSize,
		a.runtimeConfig.StreamRequestBody,
		a.resiliency,
		config.IsFeatureEnabled(a.globalConfig.Spec.Features, config.Resiliency),
	)
}

func (a *DaprRuntime) initProxy() {
	a.proxy = messaging.NewProxy(a.grpc.GetGRPCConnection, a.runtimeConfig.ID,
		fmt.Sprintf("%s:%d", channel.DefaultChannelAddress, a.runtimeConfig.ApplicationPort), a.runtimeConfig.InternalGRPCPort, a.accessControlList, a.runtimeConfig.AppSSL, a.resiliency)

	log.Info("gRPC proxy enabled")
}

// begin components updates for kubernetes mode.
func (a *DaprRuntime) beginComponentsUpdates() error {
	if a.runtimeConfig.Mode != modes.KubernetesMode {
		return nil
	}

	go func() {
		parseAndUpdate := func(compRaw []byte) {
			var component components_v1alpha1.Component
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
			var stream operatorv1pb.Operator_ComponentUpdateClient

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

func (a *DaprRuntime) onComponentUpdated(component components_v1alpha1.Component) bool {
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
				var resp *bindings.InvokeResponse
				policy := a.resiliency.ComponentOutboundPolicy(a.ctx, name)
				err := policy(func(ctx context.Context) (err error) {
					resp, err = binding.Invoke(ctx, req)
					return err
				})
				return resp, err
			}
		}
		supported := make([]string, 0, len(ops))
		for _, o := range ops {
			supported = append(supported, string(o))
		}
		return nil, errors.Errorf("binding %s does not support operation %s. supported operations:%s", name, req.Operation, strings.Join(supported, " "))
	}
	return nil, errors.Errorf("couldn't find output binding %s", name)
}

func (a *DaprRuntime) onAppResponse(response *bindings.AppResponse) error {
	if len(response.State) > 0 {
		go func(reqs []state.SetRequest) {
			if a.stateStores != nil {
				policy := a.resiliency.ComponentOutboundPolicy(a.ctx, response.StoreName)
				err := policy(func(ctx context.Context) (err error) {
					return a.stateStores[response.StoreName].BulkSet(reqs)
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
	ctx, span := diag.StartInternalCallbackSpan(a.ctx, spanName, trace.SpanContext{}, a.globalConfig.Spec.TracingSpec)

	var appResponseBody []byte
	path := a.inputBindingRoutes[bindingName]
	if path == "" {
		path = bindingName
	}

	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
		client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
		req := &runtimev1pb.BindingEventRequest{
			Name:     bindingName,
			Data:     data,
			Metadata: metadata,
		}
		start := time.Now()

		var resp *runtimev1pb.BindingEventResponse
		policy := a.resiliency.ComponentInboundPolicy(ctx, bindingName)
		err := policy(func(ctx context.Context) (err error) {
			resp, err = client.OnBindingEvent(ctx, req)
			return err
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
			var body []byte
			if resp != nil {
				body = resp.Data
			}
			return nil, errors.Wrap(err, fmt.Sprintf("error invoking app, body: %s", string(body)))
		}
		if resp != nil {
			if resp.Concurrency == runtimev1pb.BindingEventResponse_PARALLEL {
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
		req := invokev1.NewInvokeMethodRequest(path)
		req.WithHTTPExtension(nethttp.MethodPost, "")
		req.WithRawData(data, invokev1.JSONContentType)

		reqMetadata := map[string][]string{}
		for k, v := range metadata {
			reqMetadata[k] = []string{v}
		}
		req.WithMetadata(reqMetadata)

		var resp *invokev1.InvokeMethodResponse
		respErr := false
		policy := a.resiliency.ComponentInboundPolicy(ctx, bindingName)
		err := policy(func(ctx context.Context) (err error) {
			respErr = false
			resp, err = a.appChannel.InvokeMethod(ctx, req)
			if err != nil {
				return err
			}

			if resp != nil && resp.Status().Code != nethttp.StatusOK {
				respErr = true
				return errors.Errorf("Error sending binding event to application, status %d", resp.Status().Code)
			}
			return nil
		})
		if err != nil && !respErr {
			return nil, errors.Wrap(err, "error invoking app")
		}

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				fmt.Sprintf("%s /%s", nethttp.MethodPost, bindingName))
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, int(resp.Status().Code))
			span.End()
		}
		// ::TODO report metrics for http, such as grpc
		if resp.Status().Code != nethttp.StatusOK {
			_, body := resp.RawData()
			return nil, errors.Errorf("fails to send binding event to http app channel, status code: %d body: %s", resp.Status().Code, string(body))
		}

		if resp.Message().Data != nil && len(resp.Message().Data.Value) > 0 {
			appResponseBody = resp.Message().Data.Value
		}
	}

	if len(response.State) > 0 || len(response.To) > 0 {
		if err := a.onAppResponse(&response); err != nil {
			log.Errorf("error executing app response: %s", err)
		}
	}

	return appResponseBody, nil
}

func (a *DaprRuntime) readFromBinding(subscribeCtx context.Context, name string, binding bindings.InputBinding) error {
	err := binding.Read(subscribeCtx, func(ctx context.Context, resp *bindings.ReadResponse) ([]byte, error) {
		if resp != nil {
			start := time.Now()
			b, err := a.sendBindingEventToApp(name, resp.Data, resp.Metadata)
			elapsed := diag.ElapsedSince(start)

			diag.DefaultComponentMonitoring.InputBindingEvent(context.Background(), name, err == nil, elapsed)

			if err != nil {
				log.Debugf("error from app consumer for binding [%s]: %s", name, err)
				return nil, err
			}

			return b, err
		}
		return nil, nil
	})
	return err
}

func (a *DaprRuntime) startHTTPServer(port int, publicPort *int, profilePort int, allowedOrigins string, pipeline http_middleware.Pipeline) error {
	a.daprHTTPAPI = http.NewAPI(a.runtimeConfig.ID,
		a.appChannel,
		a.directMessaging,
		a.getComponents,
		a.resiliency,
		a.stateStores,
		a.lockStores,
		a.secretStores,
		a.secretsConfiguration,
		a.configurationStores,
		a.getPublishAdapter(),
		a.actor,
		a.sendToOutputBinding,
		a.globalConfig.Spec.TracingSpec,
		a.ShutdownWithWait,
		a.getComponentsCapabilitesMap,
	)
	serverConf := http.NewServerConfig(
		a.runtimeConfig.ID,
		a.hostAddress,
		port,
		a.runtimeConfig.APIListenAddresses,
		publicPort,
		profilePort,
		allowedOrigins,
		a.runtimeConfig.EnableProfiling,
		a.runtimeConfig.MaxRequestBodySize,
		a.runtimeConfig.UnixDomainSocket,
		a.runtimeConfig.ReadBufferSize,
		a.runtimeConfig.StreamRequestBody,
		a.runtimeConfig.EnableAPILogging,
	)

	server := http.NewServer(a.daprHTTPAPI,
		serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, pipeline, a.globalConfig.Spec.APISpec)
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
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, a.globalConfig.Spec.APISpec, a.proxy)
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
	return grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port, apiListenAddresses, a.namespace, trustDomain, a.runtimeConfig.MaxRequestBodySize, a.runtimeConfig.UnixDomainSocket, a.runtimeConfig.ReadBufferSize, a.runtimeConfig.EnableAPILogging)
}

func (a *DaprRuntime) getGRPCAPI() grpc.API {
	return grpc.NewAPI(a.runtimeConfig.ID,
		a.appChannel,
		a.resiliency,
		a.stateStores,
		a.secretStores,
		a.secretsConfiguration,
		a.configurationStores,
		a.lockStores,
		a.getPublishAdapter(),
		a.directMessaging,
		a.actor,
		a.sendToOutputBinding,
		a.globalConfig.Spec.TracingSpec,
		a.accessControlList,
		string(a.runtimeConfig.ApplicationProtocol),
		a.getComponents,
		a.ShutdownWithWait,
		a.getComponentsCapabilitesMap,
	)
}

func (a *DaprRuntime) getPublishAdapter() runtime_pubsub.Adapter {
	if a.pubSubs == nil || len(a.pubSubs) == 0 {
		return nil
	}

	return a
}

func (a *DaprRuntime) getSubscribedBindingsGRPC() []string {
	client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
	resp, err := client.ListInputBindings(context.Background(), &emptypb.Empty{})
	bindings := []string{}

	if err == nil && resp != nil {
		bindings = resp.Bindings
	}
	return bindings
}

func (a *DaprRuntime) isAppSubscribedToBinding(binding string) bool {
	// if gRPC, looks for the binding in the list of bindings returned from the app
	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		if a.subscribeBindingList == nil {
			a.subscribeBindingList = a.getSubscribedBindingsGRPC()
		}
		for _, b := range a.subscribeBindingList {
			if b == binding {
				return true
			}
		}
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		// if HTTP, check if there's an endpoint listening for that binding
		path := a.inputBindingRoutes[binding]
		req := invokev1.NewInvokeMethodRequest(path)
		req.WithHTTPExtension(nethttp.MethodOptions, "")
		req.WithRawData(nil, invokev1.JSONContentType)

		// TODO: Propagate Context
		ctx := context.Background()
		resp, err := a.appChannel.InvokeMethod(ctx, req)
		if err != nil {
			log.Fatalf("could not invoke OPTIONS method on input binding subscription endpoint %q: %w", path, err)
		}
		code := resp.Status().Code

		return code/100 == 2 || code == nethttp.StatusMethodNotAllowed
	}
	return false
}

func (a *DaprRuntime) initInputBinding(c components_v1alpha1.Component) error {
	binding, err := a.bindingsRegistry.CreateInputBinding(c.Spec.Type, c.Spec.Version)
	if err != nil {
		log.Warnf("failed to create input binding %s (%s/%s): %s", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}
	err = binding.Init(bindings.Metadata{
		Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
		Name:       c.ObjectMeta.Name,
	})
	if err != nil {
		log.Errorf("failed to init input binding %s (%s/%s): %s", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
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

func (a *DaprRuntime) initOutputBinding(c components_v1alpha1.Component) error {
	binding, err := a.bindingsRegistry.CreateOutputBinding(c.Spec.Type, c.Spec.Version)
	if err != nil {
		log.Warnf("failed to create output binding %s (%s/%s): %s", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	if binding != nil {
		err := binding.Init(bindings.Metadata{
			Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
			Name:       c.ObjectMeta.Name,
		})
		if err != nil {
			log.Errorf("failed to init output binding %s (%s/%s): %s", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version, err)
			diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
			return err
		}
		log.Infof("successful init for output binding %s (%s/%s)", c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
		a.outputBindings[c.ObjectMeta.Name] = binding
		diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	}
	return nil
}

func (a *DaprRuntime) initConfiguration(s components_v1alpha1.Component) error {
	store, err := a.configurationStoreRegistry.Create(s.Spec.Type, s.Spec.Version)
	if err != nil {
		log.Warnf("error creating configuration store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
		return err
	}
	if store != nil {
		props := a.convertMetadataItemsToProperties(s.Spec.Metadata)
		err := store.Init(configuration.Metadata{
			Properties: props,
		})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
			log.Warnf("error initializing configuration store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
			return err
		}

		a.configurationStores[s.ObjectMeta.Name] = store
		diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)
	}

	return nil
}

func (a *DaprRuntime) initLock(s components_v1alpha1.Component) error {
	// create the component
	store, err := a.lockStoreRegistry.Create(s.Spec.Type, s.Spec.Version)
	if err != nil {
		log.Warnf("error creating lock store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
		return err
	}
	if store == nil {
		return nil
	}
	// initialization
	props := a.convertMetadataItemsToProperties(s.Spec.Metadata)
	err = store.InitLockStore(lock.Metadata{
		Properties: props,
	})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
		log.Warnf("error initializing lock store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
		return err
	}
	// save lock related configuration
	a.lockStores[s.ObjectMeta.Name] = store
	err = lock_loader.SaveLockConfiguration(s.ObjectMeta.Name, props)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
		log.Warnf("error save lock keyprefix: %s", err.Error())
		return err
	}
	diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)

	return nil
}

// Refer for state store api decision  https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (a *DaprRuntime) initState(s components_v1alpha1.Component) error {
	store, err := a.stateStoreRegistry.Create(s.Spec.Type, s.Spec.Version)
	if err != nil {
		log.Warnf("error creating state store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
		return err
	}
	if store != nil {
		secretStoreName := a.authSecretStoreOrDefault(s)

		secretStore := a.getSecretStore(secretStoreName)
		encKeys, encErr := encryption.ComponentEncryptionKey(s, secretStore)
		if encErr != nil {
			log.Errorf("error initializing state store encryption %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, encErr)
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
			return encErr
		}

		if encKeys.Primary.Key != "" {
			ok := encryption.AddEncryptedStateStore(s.ObjectMeta.Name, encKeys)
			if ok {
				log.Infof("automatic encryption enabled for state store %s", s.ObjectMeta.Name)
			}
		}

		props := a.convertMetadataItemsToProperties(s.Spec.Metadata)
		err = store.Init(state.Metadata{
			Properties: props,
		})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
			log.Warnf("error initializing state store %s (%s/%s): %s", s.ObjectMeta.Name, s.Spec.Type, s.Spec.Version, err)
			return err
		}

		a.stateStores[s.ObjectMeta.Name] = store
		err = state_loader.SaveStateConfiguration(s.ObjectMeta.Name, props)
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
			log.Warnf("error save state keyprefix: %s", err.Error())
			return err
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

func (a *DaprRuntime) getDeclarativeSubscriptions() []runtime_pubsub.Subscription {
	var subs []runtime_pubsub.Subscription

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		subs = runtime_pubsub.DeclarativeKubernetes(a.operatorClient, a.podName, a.namespace, log)
	case modes.StandaloneMode:
		subs = runtime_pubsub.DeclarativeSelfHosted(a.runtimeConfig.Standalone.ComponentsPath, log)
	}

	// only return valid subscriptions for this app id
	for i := len(subs) - 1; i >= 0; i-- {
		s := subs[i]
		if len(s.Scopes) == 0 {
			continue
		}

		found := false
		for _, scope := range s.Scopes {
			if scope == a.runtimeConfig.ID {
				found = true
				break
			}
		}

		if !found {
			subs = append(subs[:i], subs[i+1:]...)
		}
	}
	return subs
}

func (a *DaprRuntime) getTopicRoutes() (map[string]TopicRoute, error) {
	if a.topicRoutes != nil {
		return a.topicRoutes, nil
	}

	topicRoutes := make(map[string]TopicRoute)
	deadLetterTopics := make(map[string]string)

	if a.appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return topicRoutes, nil
	}

	var subscriptions []runtime_pubsub.Subscription
	var err error

	// handle app subscriptions
	resiliencyEnabled := config.IsFeatureEnabled(a.globalConfig.Spec.Features, config.Resiliency)
	if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		subscriptions, err = runtime_pubsub.GetSubscriptionsHTTP(a.appChannel, log, a.resiliency, resiliencyEnabled)
	} else if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
		subscriptions, err = runtime_pubsub.GetSubscriptionsGRPC(client, log, a.resiliency, resiliencyEnabled)
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

	for _, s := range subscriptions {
		if _, ok := topicRoutes[s.PubsubName]; !ok {
			topicRoutes[s.PubsubName] = TopicRoute{routes: make(map[string]Route)}
		}

		topicRoutes[s.PubsubName].routes[s.Topic] = Route{metadata: s.Metadata, rules: s.Rules}
		if len(s.DeadLetterTopic) > 0 {
			deadLetterTopics[fmt.Sprintf(deadLetterKeyFormat, s.PubsubName, s.Topic)] = s.DeadLetterTopic
		}
	}

	if len(topicRoutes) > 0 {
		for pubsubName, v := range topicRoutes {
			topics := []string{}
			for topic := range v.routes {
				topics = append(topics, topic)
			}
			log.Infof("app is subscribed to the following topics: %v through pubsub=%s", topics, pubsubName)
		}
	}
	a.topicRoutes = topicRoutes
	a.deadLetterTopics = deadLetterTopics
	return topicRoutes, nil
}

func (a *DaprRuntime) initPubSub(c components_v1alpha1.Component) error {
	pubSub, err := a.pubSubRegistry.Create(c.Spec.Type, c.Spec.Version)
	if err != nil {
		log.Warnf("error creating pub sub %s (%s/%s): %s", &c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	properties := a.convertMetadataItemsToProperties(c.Spec.Metadata)
	consumerID := strings.TrimSpace(properties["consumerID"])
	if consumerID == "" {
		consumerID = a.runtimeConfig.ID
	}
	properties["consumerID"] = consumerID

	err = pubSub.Init(pubsub.Metadata{
		Properties: properties,
	})
	if err != nil {
		log.Warnf("error initializing pub sub %s/%s: %s", c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
	}

	pubsubName := c.ObjectMeta.Name

	a.scopedSubscriptions[pubsubName] = scopes.GetScopedTopics(scopes.SubscriptionScopes, a.runtimeConfig.ID, properties)
	a.scopedPublishings[pubsubName] = scopes.GetScopedTopics(scopes.PublishingScopes, a.runtimeConfig.ID, properties)
	a.allowedTopics[pubsubName] = scopes.GetAllowedTopics(properties)
	a.pubSubs[pubsubName] = pubSub
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)

	return nil
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (a *DaprRuntime) Publish(req *pubsub.PublishRequest) error {
	thepubsub := a.GetPubSub(req.PubsubName)
	if thepubsub == nil {
		return runtime_pubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, a.scopedPublishings[req.PubsubName]); !allowed {
		return runtime_pubsub.NotAllowedError{Topic: req.Topic, ID: a.runtimeConfig.ID}
	}

	policy := a.resiliency.ComponentOutboundPolicy(a.ctx, req.PubsubName)
	return policy(func(ctx context.Context) (err error) {
		return a.pubSubs[req.PubsubName].Publish(req)
	})
}

// GetPubSub is an adapter method to find a pubsub by name.
func (a *DaprRuntime) GetPubSub(pubsubName string) pubsub.PubSub {
	return a.pubSubs[pubsubName]
}

func (a *DaprRuntime) isPubSubOperationAllowed(pubsubName string, topic string, scopedTopics []string) bool {
	inAllowedTopics := false

	// first check if allowedTopics contain it
	if len(a.allowedTopics[pubsubName]) > 0 {
		for _, t := range a.allowedTopics[pubsubName] {
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
			return errors.Errorf("unable to determine name resolver for %s mode", string(a.runtimeConfig.Mode))
		}
	}

	if resolverVersion == "" {
		resolverVersion = components.FirstStableVersion
	}

	resolver, err = a.nameResolutionRegistry.Create(resolverName, resolverVersion)
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
		log.Warnf("error creating name resolution resolver %s: %s", resolverName, err)
		return err
	}

	if err = resolver.Init(resolverMetadata); err != nil {
		log.Errorf("failed to initialize name resolution resolver %s: %s", resolverName, err)
		return err
	}

	a.nameResolver = resolver

	log.Infof("Initialized name resolution to %s", resolverName)
	return nil
}

func (a *DaprRuntime) publishMessageHTTP(ctx context.Context, msg *pubsubSubscribedMessage) error {
	cloudEvent := msg.cloudEvent

	var span trace.Span

	req := invokev1.NewInvokeMethodRequest(msg.path)
	req.WithHTTPExtension(nethttp.MethodPost, "")
	req.WithRawData(msg.data, contenttype.CloudEventContentType)
	req.WithCustomHTTPMetadata(msg.metadata)

	if cloudEvent[pubsub.TraceIDField] != nil {
		traceID := cloudEvent[pubsub.TraceIDField].(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		spanName := fmt.Sprintf("pubsub/%s", msg.topic)
		ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, a.globalConfig.Spec.TracingSpec)
	}

	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return errors.Wrap(err, "error from app channel while sending pub/sub event to app")
	}

	statusCode := int(resp.Status().Code)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	_, body := resp.RawData()

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse pubsub.AppResponse
		err := json.Unmarshal(body, &appResponse)
		if err != nil {
			log.Debugf("skipping status check due to error parsing result from pub/sub event %v", cloudEvent[pubsub.IDField])
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
			return errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
		case pubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
			return nil
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return errors.Errorf("unknown status returned from app while processing pub/sub event %v: %v", cloudEvent[pubsub.IDField], appResponse.Status)
	}

	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent[pubsub.IDField], body, statusCode)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
		return nil
	}

	// Every error from now on is a retriable error.
	log.Warnf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.TopicField], body, statusCode)
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	return errors.Errorf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.TopicField], body, statusCode)
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
			ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
		} else {
			log.Warnf("ignored non-string traceid value: %v", iTraceID)
		}
	}

	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.metadata)

	clientV1 := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)

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

		err = errors.Errorf("error returned from app while processing pub/sub event %v: %s", cloudEvent[pubsub.IDField], err)
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)

		// on error from application, return error for redelivery of event
		return err
	}

	switch res.GetStatus() {
	case runtimev1pb.TopicEventResponse_SUCCESS:
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
		return nil
	case runtimev1pb.TopicEventResponse_RETRY:
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		return errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
	case runtimev1pb.TopicEventResponse_DROP:
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[pubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)

		return nil
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	return errors.Errorf("unknown status returned from app while processing pub/sub event %v: %v", cloudEvent[pubsub.IDField], res.GetStatus())
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
		return err
	}
	a.actorStateStoreLock.Lock()
	defer a.actorStateStoreLock.Unlock()
	if a.actorStateStoreName == "" {
		log.Info("actors: state store is not configured - this is okay for clients but services with hosted actors will fail to initialize!")
	}
	actorConfig := actors.NewConfig(a.hostAddress, a.runtimeConfig.ID,
		a.runtimeConfig.PlacementAddresses, a.runtimeConfig.InternalGRPCPort,
		a.namespace, a.appConfig)
	act := actors.NewActors(a.stateStores[a.actorStateStoreName], a.appChannel, a.grpc.GetGRPCConnection, actorConfig,
		a.runtimeConfig.CertChain, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.Features,
		a.resiliency, a.actorStateStoreName)
	err = act.Init()
	if err == nil {
		a.actor = act
	}
	return err
}

func (a *DaprRuntime) getAuthorizedComponents(components []components_v1alpha1.Component) []components_v1alpha1.Component {
	authorized := make([]components_v1alpha1.Component, len(components))

	i := 0
	for _, c := range components {
		if a.isComponentAuthorized(c) {
			authorized[i] = c
			i++
		}
	}
	return authorized[0:i]
}

func (a *DaprRuntime) isComponentAuthorized(component components_v1alpha1.Component) bool {
	for _, auth := range a.componentAuthorizers {
		if !auth(component) {
			return false
		}
	}
	return true
}

func (a *DaprRuntime) namespaceComponentAuthorizer(component components_v1alpha1.Component) bool {
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
		return errors.Errorf("components loader for mode %s not found", a.runtimeConfig.Mode)
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

	for _, comp := range authorizedComps {
		a.pendingComponents <- comp
	}

	return nil
}

func (a *DaprRuntime) appendOrReplaceComponents(component components_v1alpha1.Component) {
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

func (a *DaprRuntime) extractComponentCategory(component components_v1alpha1.Component) ComponentCategory {
	for _, category := range componentCategoriesNeedProcess {
		if strings.HasPrefix(component.Spec.Type, fmt.Sprintf("%s.", category)) {
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
	a.pendingComponents <- components_v1alpha1.Component{}
	log.Info("all outstanding components processed")
}

func (a *DaprRuntime) processComponentAndDependents(comp components_v1alpha1.Component) error {
	log.Debugf("loading component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
	res := a.preprocessOneComponent(&comp)
	if res.unreadyDependency != "" {
		a.pendingComponentDependents[res.unreadyDependency] = append(a.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := a.extractComponentCategory(comp)
	if compCategory == "" {
		// the category entered is incorrect, return error
		return errors.Errorf("incorrect type %s", comp.Spec.Type)
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
		return fmt.Errorf("init timeout for component %s exceeded after %s", comp.Name, timeout.String())
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

func (a *DaprRuntime) doProcessOneComponent(category ComponentCategory, comp components_v1alpha1.Component) error {
	switch category {
	case bindingsComponent:
		return a.initBinding(comp)
	case pubsubComponent:
		return a.initPubSub(comp)
	case secretStoreComponent:
		return a.initSecretStore(comp)
	case stateComponent:
		return a.initState(comp)
	case configurationComponent:
		return a.initConfiguration(comp)
	case lockComponent:
		return a.initLock(comp)
	}
	return nil
}

func (a *DaprRuntime) preprocessOneComponent(comp *components_v1alpha1.Component) componentPreprocessRes {
	var unreadySecretsStore string
	*comp, unreadySecretsStore = a.processComponentSecrets(*comp)
	if unreadySecretsStore != "" {
		return componentPreprocessRes{
			unreadyDependency: componentDependency(secretStoreComponent, unreadySecretsStore),
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

// shutdownOutputComponents allows for a graceful shutdown of all runtime internal operations of components that are not source of more work.
// These are all components except input bindings and pubsub.
func (a *DaprRuntime) shutdownOutputComponents() error {
	log.Info("Shutting down all remaining components")
	var merr error

	// Close components if they implement `io.Closer`
	// Input bindings are closed when a.ctx is canceled
	for name, binding := range a.outputBindings {
		if closer, ok := binding.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				err = fmt.Errorf("error closing output binding %s: %w", name, err)
				merr = multierror.Append(merr, err)
				log.Warn(err)
			}
		}
	}
	for name, secretstore := range a.secretStores {
		if closer, ok := secretstore.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				err = fmt.Errorf("error closing secret store %s: %w", name, err)
				merr = multierror.Append(merr, err)
				log.Warn(err)
			}
		}
	}
	for name, stateStore := range a.stateStores {
		if closer, ok := stateStore.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				err = fmt.Errorf("error closing state store %s: %w", name, err)
				merr = multierror.Append(merr, err)
				log.Warn(err)
			}
		}
	}
	// Close pubsub publisher
	// The subscriber part is closed when a.ctx is canceled
	for name, pubSub := range a.pubSubs {
		if err := pubSub.Close(); err != nil {
			err = fmt.Errorf("error closing pub sub %s: %w", name, err)
			merr = multierror.Append(merr, err)
			log.Warn(err)
		}
	}
	if closer, ok := a.nameResolver.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			err = fmt.Errorf("error closing name resolver: %w", err)
			merr = multierror.Append(merr, err)
			log.Warn(err)
		}
	}

	return merr
}

// ShutdownWithWait will gracefully stop runtime and wait outstanding operations.
func (a *DaprRuntime) ShutdownWithWait() {
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
	// Ensure the Unix socket file is removed if a panic occurs.
	defer a.cleanSocket()

	log.Infof("dapr shutting down.")

	log.Infof("Stopping PubSub subscribers and input bindings")
	a.cancel()
	a.stopActor()
	log.Info("Stopping Dapr APIs")
	for _, closer := range a.apiClosers {
		if err := closer.Close(); err != nil {
			log.Warnf("error closing API: %v", err)
		}
	}
	if a.tracerProvider != nil {
		a.tracerProvider.Shutdown(context.Background())
	}
	log.Infof("Waiting %s to finish outstanding operations", duration)
	<-time.After(duration)
	a.shutdownOutputComponents()
	a.shutdownC <- nil
}

func (a *DaprRuntime) WaitUntilShutdown() error {
	return <-a.shutdownC
}

func (a *DaprRuntime) processComponentSecrets(component components_v1alpha1.Component) (components_v1alpha1.Component, string) {
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
		if a.runtimeConfig.Mode == modes.KubernetesMode && secretStoreName == secretstores_loader.BuiltinKubernetesSecretStore {
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

			m.Value = components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: dec,
				},
			}

			component.Spec.Metadata[i] = m
			continue
		}

		resp, ok := cache[m.SecretKeyRef.Name]
		if !ok {
			r, err := secretStore.GetSecret(secretstores.GetSecretRequest{
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
			component.Spec.Metadata[i].Value = components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(val),
				},
			}
		}

		cache[m.SecretKeyRef.Name] = resp
	}
	return component, ""
}

func (a *DaprRuntime) authSecretStoreOrDefault(comp components_v1alpha1.Component) string {
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

	for {
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(a.runtimeConfig.ApplicationPort)), time.Millisecond*500)
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

func (a *DaprRuntime) createAppChannel() error {
	if a.runtimeConfig.ApplicationPort > 0 {
		var channelCreatorFn func(port, maxConcurrency int, spec config.TracingSpec, sslEnabled bool, maxRequestBodySize int, readBufferSize int) (channel.AppChannel, error)

		switch a.runtimeConfig.ApplicationProtocol {
		case GRPCProtocol:
			channelCreatorFn = a.grpc.CreateLocalChannel
		case HTTPProtocol:
			channelCreatorFn = http_channel.CreateLocalChannel
		default:
			return errors.Errorf("cannot create app channel for protocol %s", string(a.runtimeConfig.ApplicationProtocol))
		}

		ch, err := channelCreatorFn(a.runtimeConfig.ApplicationPort, a.runtimeConfig.MaxConcurrency, a.globalConfig.Spec.TracingSpec, a.runtimeConfig.AppSSL, a.runtimeConfig.MaxRequestBodySize, a.runtimeConfig.ReadBufferSize)
		if err != nil {
			log.Infof("app max concurrency set to %v", a.runtimeConfig.MaxConcurrency)
		}

		// TODO: Remove once feature is finalized
		if a.runtimeConfig.ApplicationProtocol == HTTPProtocol && !config.GetNoDefaultContentType() {
			log.Warn("[DEPRECATION NOTICE] Adding a default content type to incoming service invocation requests is deprecated and will be removed in the future. See https://docs.dapr.io/operations/support/support-preview-features/ for more details. You can opt into the new behavior today by setting the configuration option `ServiceInvocation.NoDefaultContentType` to true.")
		}
		a.appChannel = ch
	} else {
		log.Warn("app channel is not initialized. did you make sure to configure an app-port?")
	}

	return nil
}

func (a *DaprRuntime) appendBuiltinSecretStore() {
	if a.runtimeConfig.DisableBuiltinK8sSecretStore {
		return
	}

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		// Preload Kubernetes secretstore
		a.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstores_loader.BuiltinKubernetesSecretStore,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: components.FirstStableVersion,
			},
		}
	}
}

func (a *DaprRuntime) initSecretStore(c components_v1alpha1.Component) error {
	secretStore, err := a.secretStoresRegistry.Create(c.Spec.Type, c.Spec.Version)
	if err != nil {
		log.Warnf("failed to create secret store %s/%s: %s", c.Spec.Type, c.Spec.Version, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	err = secretStore.Init(secretstores.Metadata{
		Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
	})
	if err != nil {
		log.Warnf("failed to init secret store %s/%s named %s: %s", c.Spec.Type, c.Spec.Version, c.ObjectMeta.Name, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
	}

	a.secretStores[c.ObjectMeta.Name] = secretStore
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) convertMetadataItemsToProperties(items []components_v1alpha1.MetadataItem) map[string]string {
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
		properties[c.Name] = val
	}
	return properties
}

func (a *DaprRuntime) getComponent(componentType string, name string) (components_v1alpha1.Component, bool) {
	a.componentsLock.RLock()
	defer a.componentsLock.RUnlock()

	for i, c := range a.components {
		if c.Spec.Type == componentType && c.ObjectMeta.Name == name {
			return a.components[i], true
		}
	}
	return components_v1alpha1.Component{}, false
}

func (a *DaprRuntime) getComponents() []components_v1alpha1.Component {
	a.componentsLock.RLock()
	defer a.componentsLock.RUnlock()

	comps := make([]components_v1alpha1.Component, len(a.components))
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

func componentDependency(compCategory ComponentCategory, name string) string {
	return fmt.Sprintf("%s:%s", compCategory, name)
}

func (a *DaprRuntime) startSubscribing() {
	// PubSub subscribers are stopped via cancelation of the main runtime's context
	for name, pubsub := range a.pubSubs {
		if err := a.beginPubSub(a.ctx, name, pubsub); err != nil {
			log.Errorf("error occurred while beginning pubsub %s: %s", name, err)
		}
	}
}

func (a *DaprRuntime) startReadingFromBindings() (err error) {
	if a.appChannel == nil {
		return errors.New("app channel not initialized")
	}
	for name, binding := range a.inputBindings {
		if !a.isAppSubscribedToBinding(name) {
			log.Infof("app has not subscribed to binding %s.", name)
			continue
		}

		err = a.readFromBinding(a.ctx, name, binding)
		if err != nil {
			log.Errorf("error reading from input binding %s: %s", name, err)
			continue
		}
	}
	return nil
}
