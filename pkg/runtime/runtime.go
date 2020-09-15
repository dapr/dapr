// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/middleware"
	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	http_channel "github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/components"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"
	exporter_loader "github.com/dapr/dapr/pkg/components/exporters"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	nr_loader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/logger"
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
	"github.com/golang/protobuf/ptypes/empty"
	jsoniter "github.com/json-iterator/go"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	appConfigEndpoint = "dapr/config"
	actorStateStore   = "actorStateStore"

	// output bindings concurrency
	bindingsConcurrnecyParallel   = "parallel"
	bindingsConcurrnecySequential = "sequential"
)

type ComponentCategory string

const (
	bindingsComponent    ComponentCategory = "bindings"
	exporterComponent    ComponentCategory = "exporter"
	pubsubComponent      ComponentCategory = "pubsub"
	secretStoreComponent ComponentCategory = "secretstores"
	stateComponent       ComponentCategory = "state"
)

var componentCategoriesNeedProcess = []ComponentCategory{
	bindingsComponent,
	exporterComponent,
	pubsubComponent,
	secretStoreComponent,
	stateComponent,
}

var log = logger.NewLogger("dapr.runtime")

type TopicRoute struct {
	routes map[string]string
}

// DaprRuntime holds all the core components of the runtime
type DaprRuntime struct {
	runtimeConfig          *Config
	globalConfig           *config.Configuration
	components             []components_v1alpha1.Component
	grpc                   *grpc.Manager
	appChannel             channel.AppChannel
	appConfig              config.ApplicationConfig
	directMessaging        messaging.DirectMessaging
	stateStoreRegistry     state_loader.Registry
	secretStoresRegistry   secretstores_loader.Registry
	exporterRegistry       exporter_loader.Registry
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
	json                   jsoniter.API
	httpMiddlewareRegistry http_middleware_loader.Registry
	hostAddress            string
	actorStateStoreName    string
	actorStateStoreCount   int
	authenticator          security.Authenticator
	namespace              string
	scopedSubscriptions    map[string][]string
	scopedPublishings      map[string][]string
	allowedTopics          map[string][]string
	daprHTTPAPI            http.API
	operatorClient         operatorv1pb.OperatorClient
	topicRoutes            map[string]TopicRoute

	pendingComponents          chan components_v1alpha1.Component
	pendingComponentsDone      chan bool
	pendingComponentDependents map[string][]components_v1alpha1.Component
}

type componentPreprocessRes struct {
	unreadyDependency string
}

// NewDaprRuntime returns a new runtime with the given runtime config and global config
func NewDaprRuntime(runtimeConfig *Config, globalConfig *config.Configuration) *DaprRuntime {
	return &DaprRuntime{
		runtimeConfig:          runtimeConfig,
		globalConfig:           globalConfig,
		grpc:                   grpc.NewGRPCManager(runtimeConfig.Mode),
		json:                   jsoniter.ConfigFastest,
		inputBindings:          map[string]bindings.InputBinding{},
		outputBindings:         map[string]bindings.OutputBinding{},
		secretStores:           map[string]secretstores.SecretStore{},
		stateStores:            map[string]state.Store{},
		pubSubs:                map[string]pubsub.PubSub{},
		stateStoreRegistry:     state_loader.NewRegistry(),
		bindingsRegistry:       bindings_loader.NewRegistry(),
		pubSubRegistry:         pubsub_loader.NewRegistry(),
		secretStoresRegistry:   secretstores_loader.NewRegistry(),
		exporterRegistry:       exporter_loader.NewRegistry(),
		nameResolutionRegistry: nr_loader.NewRegistry(),
		httpMiddlewareRegistry: http_middleware_loader.NewRegistry(),

		scopedSubscriptions: map[string][]string{},
		scopedPublishings:   map[string][]string{},
		allowedTopics:       map[string][]string{},

		pendingComponents:          make(chan components_v1alpha1.Component),
		pendingComponentsDone:      make(chan bool),
		pendingComponentDependents: map[string][]components_v1alpha1.Component{},
	}
}

// Run performs initialization of the runtime with the runtime and global configurations
func (a *DaprRuntime) Run(opts ...Option) error {
	start := time.Now().UTC()
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

	d := time.Since(start).Seconds() * 1000
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

func (a *DaprRuntime) initRuntime(opts *runtimeOpts) error {
	err := a.establishSecurity(a.runtimeConfig.SentryServiceAddress)
	if err != nil {
		return err
	}
	a.namespace = a.getNamespace()
	a.operatorClient, err = a.getOperatorClient()
	if err != nil {
		return err
	}

	a.blockUntilAppIsReady()

	a.hostAddress, err = utils.GetHostAddress()
	if err != nil {
		return errors.Wrap(err, "failed to determine host address")
	}

	if a.globalConfig.Spec.TracingSpec.Stdout {
		trace.RegisterExporter(&diag_utils.StdoutExporter{})
	}

	err = a.createAppChannel()
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.ApplicationProtocol), err)
	}

	a.loadAppConfiguration()

	// Register and initialize name resolution for service discovery.
	a.nameResolutionRegistry.Register(opts.nameResolutions...)
	err = a.initNameResolution()
	if err != nil {
		log.Warnf("failed to init name resolution: %s", err)
	}

	a.exporterRegistry.Register(opts.exporters...)
	a.pubSubRegistry.Register(opts.pubsubs...)
	a.secretStoresRegistry.Register(opts.secretStores...)
	a.stateStoreRegistry.Register(opts.states...)
	a.bindingsRegistry.RegisterInputBindings(opts.inputBindings...)
	a.bindingsRegistry.RegisterOutputBindings(opts.outputBindings...)

	go a.processComponents()
	err = a.beginComponentsUpdates()
	if err != nil {
		log.Warnf("failed to watch component updates: %s", err)
	}
	a.appendBuiltinSecretStore()
	err = a.loadComponents(opts)
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}

	a.flushOutstandingComponents()

	a.initDirectMessaging(a.nameResolver)

	err = a.initActors()
	if err != nil {
		log.Warnf("failed to init actors: %s", err)
	}

	// Register and initialize HTTP middleware
	a.httpMiddlewareRegistry.Register(opts.httpMiddleware...)
	pipeline, err := a.buildHTTPPipeline()
	if err != nil {
		log.Warnf("failed to build HTTP pipeline: %s", err)
	}

	// Create and start internal and external gRPC servers
	grpcAPI := a.getGRPCAPI()
	err = a.startGRPCAPIServer(grpcAPI, a.runtimeConfig.APIGRPCPort)
	if err != nil {
		log.Fatalf("failed to start API gRPC server: %s", err)
	}
	log.Infof("API gRPC server is running on port %v", a.runtimeConfig.APIGRPCPort)

	err = a.startGRPCInternalServer(grpcAPI, a.runtimeConfig.InternalGRPCPort)
	if err != nil {
		log.Fatalf("failed to start internal gRPC server: %s", err)
	}
	log.Infof("internal gRPC server is running on port %v", a.runtimeConfig.InternalGRPCPort)

	// Start HTTP Server
	a.startHTTPServer(a.runtimeConfig.HTTPPort, a.runtimeConfig.ProfilePort, a.runtimeConfig.AllowedOrigins, pipeline)
	log.Infof("http server is running on port %v", a.runtimeConfig.HTTPPort)

	return nil
}

func (a *DaprRuntime) buildHTTPPipeline() (http_middleware.Pipeline, error) {
	var handlers []http_middleware.Middleware

	if a.globalConfig != nil {
		for i := 0; i < len(a.globalConfig.Spec.HTTPPipelineSpec.Handlers); i++ {
			middlewareSpec := a.globalConfig.Spec.HTTPPipelineSpec.Handlers[i]
			component := a.getComponent(middlewareSpec.Type, middlewareSpec.Name)
			if component == nil {
				return http_middleware.Pipeline{}, errors.Errorf("couldn't find middleware component with name %s and type %s",
					middlewareSpec.Name,
					middlewareSpec.Type)
			}
			handler, err := a.httpMiddlewareRegistry.Create(middlewareSpec.Type,
				middleware.Metadata{Properties: a.convertMetadataItemsToProperties(component.Spec.Metadata)})
			if err != nil {
				return http_middleware.Pipeline{}, err
			}
			log.Infof("enabled %s http middleware", middlewareSpec.Type)
			handlers = append(handlers, handler)
		}
	}
	return http_middleware.Pipeline{Handlers: handlers}, nil
}

func (a *DaprRuntime) initBinding(c components_v1alpha1.Component) error {
	if err := a.initOutputBinding(c); err != nil {
		log.Errorf("failed to init output bindings: %s", err)
		return err
	}

	if err := a.initInputBinding(c); err != nil {
		log.Errorf("failed to init input bindings: %s", err)
		return err
	}
	return nil
}

func (a *DaprRuntime) beginPubSub(name string, ps pubsub.PubSub) error {
	var publishFunc func(msg *pubsub.NewMessage) error
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
	for topic := range v.routes {
		allowed := a.isPubSubOperationAllowed(name, topic, a.scopedSubscriptions[name])
		if !allowed {
			log.Warnf("subscription to topic %s on pubsub %s is not allowed", topic, name)
			continue
		}

		log.Debugf("subscribing to topic=%s on pubsub=%s", topic, name)

		if err := ps.Subscribe(pubsub.SubscribeRequest{
			Topic: topic,
		}, publishFunc); err != nil {
			log.Warnf("failed to subscribe to topic %s: %s", topic, err)
		}
	}

	return nil
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
		a.globalConfig.Spec.TracingSpec)
}

func (a *DaprRuntime) beginComponentsUpdates() error {
	if a.runtimeConfig.Mode != modes.KubernetesMode {
		return nil
	}

	go func() {
		stream, err := a.operatorClient.ComponentUpdate(context.Background(), &empty.Empty{})
		if err != nil {
			log.Errorf("error from operator stream: %s", err)
			return
		}
		for {
			c, err := stream.Recv()
			if err != nil {
				log.Errorf("error from operator stream: %s", err)
				return
			}
			log.Debug("received component update")

			var component components_v1alpha1.Component
			err = json.Unmarshal(c.GetComponent(), &component)
			if err != nil {
				log.Warnf("error deserializing component: %s", err)
				continue
			}
			a.onComponentUpdated(component)
		}
	}()
	return nil
}

func (a *DaprRuntime) onComponentUpdated(component components_v1alpha1.Component) {
	existed := a.getComponent(component.Spec.Type, component.Name)
	if existed != nil && reflect.DeepEqual(existed.Spec.Metadata, component.Spec.Metadata) {
		return
	}
	a.pendingComponents <- component
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
				return binding.Invoke(req)
			}
		}
		supported := make([]string, len(ops))
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
				err := a.stateStores[response.StoreName].BulkSet(reqs)
				if err != nil {
					log.Errorf("error saving state from app response: %s", err)
				}
			}
		}(response.State)
	}

	if len(response.To) > 0 {
		b, err := a.json.Marshal(&response.Data)
		if err != nil {
			return err
		}

		if response.Concurrency == bindingsConcurrnecyParallel {
			a.sendBatchOutputBindingsParallel(response.To, b)
		} else {
			return a.sendBatchOutputBindingsSequential(response.To, b)
		}
	}

	return nil
}

func (a *DaprRuntime) sendBindingEventToApp(bindingName string, data []byte, metadata map[string]string) error {
	var response bindings.AppResponse
	spanName := fmt.Sprintf("bindings/%s", bindingName)
	ctx, span := diag.StartInternalCallbackSpan(spanName, trace.SpanContext{}, a.globalConfig.Spec.TracingSpec)

	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
		client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
		req := &runtimev1pb.BindingEventRequest{
			Name:     bindingName,
			Data:     data,
			Metadata: metadata,
		}
		resp, err := client.OnBindingEvent(ctx, req)
		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				"/dapr.proto.runtime.v1.AppCallback/OnBindingEvent")
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromGRPCError(span, err)
			span.End()
		}

		if err != nil {
			return errors.Wrap(err, "error invoking app")
		}
		if resp != nil {
			if resp.Concurrency == runtimev1pb.BindingEventResponse_PARALLEL {
				response.Concurrency = bindingsConcurrnecyParallel
			} else {
				response.Concurrency = bindingsConcurrnecySequential
			}

			response.To = resp.To

			if resp.Data != nil {
				var d interface{}
				err := a.json.Unmarshal(resp.Data, &d)
				if err == nil {
					response.Data = d
				}
			}

			for _, s := range resp.States {
				var i interface{}
				a.json.Unmarshal(s.Value, &i)

				response.State = append(response.State, state.SetRequest{
					Key:   s.Key,
					Value: i,
				})
			}
		}
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		req := invokev1.NewInvokeMethodRequest(bindingName)
		req.WithHTTPExtension(nethttp.MethodPost, "")
		req.WithRawData(data, invokev1.JSONContentType)

		reqMetadata := map[string][]string{}
		for k, v := range metadata {
			reqMetadata[k] = []string{v}
		}
		req.WithMetadata(reqMetadata)

		resp, err := a.appChannel.InvokeMethod(ctx, req)
		if err != nil {
			return errors.Wrap(err, "error invoking app")
		}

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				fmt.Sprintf("%s /%s", nethttp.MethodPost, bindingName))
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, int(resp.Status().Code))
			span.End()
		}

		if resp.Status().Code != nethttp.StatusOK {
			return errors.Errorf("fails to send binding event to http app channel, status code: %d", resp.Status().Code)
		}

		// TODO: Do we need to check content-type?
		if err := a.json.Unmarshal(resp.Message().Data.Value, &response); err != nil {
			log.Debugf("error deserializing app response: %s", err)
		}
	}

	if len(response.State) > 0 || len(response.To) > 0 {
		if err := a.onAppResponse(&response); err != nil {
			log.Errorf("error executing app response: %s", err)
		}
	}
	return nil
}

func (a *DaprRuntime) readFromBinding(name string, binding bindings.InputBinding) error {
	err := binding.Read(func(resp *bindings.ReadResponse) error {
		if resp != nil {
			err := a.sendBindingEventToApp(name, resp.Data, resp.Metadata)
			if err != nil {
				log.Debugf("error from app consumer for binding [%s]: %s", name, err)
				return err
			}
		}
		return nil
	})
	return err
}

func (a *DaprRuntime) startHTTPServer(port, profilePort int, allowedOrigins string, pipeline http_middleware.Pipeline) {
	a.daprHTTPAPI = http.NewAPI(a.runtimeConfig.ID, a.appChannel, a.directMessaging, a.stateStores, a.secretStores, a.getPublishAdapter(), a.actor, a.sendToOutputBinding, a.globalConfig.Spec.TracingSpec)
	serverConf := http.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port, profilePort, allowedOrigins, a.runtimeConfig.EnableProfiling)

	server := http.NewServer(a.daprHTTPAPI, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, pipeline)
	server.StartNonBlocking()
}

func (a *DaprRuntime) startGRPCInternalServer(api grpc.API, port int) error {
	serverConf := grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port)
	server := grpc.NewInternalServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec, a.authenticator)
	err := server.StartNonBlocking()
	return err
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port)
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.globalConfig.Spec.MetricSpec)
	err := server.StartNonBlocking()
	return err
}

func (a *DaprRuntime) getGRPCAPI() grpc.API {
	return grpc.NewAPI(a.runtimeConfig.ID, a.appChannel, a.stateStores, a.secretStores, a.getPublishAdapter(), a.directMessaging, a.actor, a.sendToOutputBinding, a.globalConfig.Spec.TracingSpec)
}

func (a *DaprRuntime) getPublishAdapter() func(*pubsub.PublishRequest) error {
	if a.pubSubs == nil || len(a.pubSubs) == 0 {
		return nil
	}

	return a.Publish
}

func (a *DaprRuntime) getSubscribedBindingsGRPC() []string {
	client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
	resp, err := client.ListInputBindings(context.Background(), &empty.Empty{})
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
		req := invokev1.NewInvokeMethodRequest(binding)
		req.WithHTTPExtension(nethttp.MethodOptions, "")
		req.WithRawData(nil, invokev1.JSONContentType)

		// TODO: Propagate Context
		ctx := context.Background()
		resp, err := a.appChannel.InvokeMethod(ctx, req)
		return err == nil && resp.Status().Code != nethttp.StatusNotFound
	}
	return false
}

func (a *DaprRuntime) initInputBinding(c components_v1alpha1.Component) error {
	if a.appChannel == nil {
		return errors.New("app channel not initialized")
	}
	subscribed := a.isAppSubscribedToBinding(c.ObjectMeta.Name)
	if !subscribed {
		return nil
	}

	binding, err := a.bindingsRegistry.CreateInputBinding(c.Spec.Type)
	if err != nil {
		log.Warnf("failed to create input binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}
	err = binding.Init(bindings.Metadata{
		Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
		Name:       c.ObjectMeta.Name,
	})
	if err != nil {
		log.Errorf("failed to init input binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
	}

	log.Infof("successful init for input binding %s (%s)", c.ObjectMeta.Name, c.Spec.Type)
	if _, ok := a.inputBindings[c.Name]; !ok {
		go func() {
			err := a.readFromBinding(c.Name, binding)
			if err != nil {
				log.Errorf("error reading from input binding %s: %s", c.Name, err)
			}
		}()
	}
	a.inputBindings[c.Name] = binding
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) initOutputBinding(c components_v1alpha1.Component) error {
	binding, err := a.bindingsRegistry.CreateOutputBinding(c.Spec.Type)
	if err != nil {
		log.Warnf("failed to create output binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	if binding != nil {
		err := binding.Init(bindings.Metadata{
			Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
			Name:       c.ObjectMeta.Name,
		})
		if err != nil {
			log.Errorf("failed to init output binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
			diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
			return err
		}
		log.Infof("successful init for output binding %s (%s)", c.ObjectMeta.Name, c.Spec.Type)
		a.outputBindings[c.ObjectMeta.Name] = binding
		diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	}
	return nil
}

// Refer for state store api decision  https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (a *DaprRuntime) initState(s components_v1alpha1.Component) error {
	store, err := a.stateStoreRegistry.CreateStateStore(s.Spec.Type)
	if err != nil {
		log.Warnf("error creating state store %s: %s", s.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
		return err
	}
	if store != nil {
		props := a.convertMetadataItemsToProperties(s.Spec.Metadata)
		err := store.Init(state.Metadata{
			Properties: props,
		})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
			log.Warnf("error initializing state store %s: %s", s.Spec.Type, err)
			return err
		}

		a.stateStores[s.ObjectMeta.Name] = store

		// set specified actor store if "actorStateStore" is true in the spec.
		actorStoreSpecified := props[actorStateStore]
		if actorStoreSpecified == "true" {
			if a.actorStateStoreCount++; a.actorStateStoreCount == 1 {
				a.actorStateStoreName = s.ObjectMeta.Name
			}
		}
		diag.DefaultMonitoring.ComponentInitialized(s.Spec.Type)
	}

	if a.actorStateStoreName == "" || a.actorStateStoreCount != 1 {
		log.Warnf("either no actor state store or multiple actor state stores are specified in the configuration, actor stores specified: %d", a.actorStateStoreCount)
	}

	return nil
}

func (a *DaprRuntime) getDeclarativeSubscriptions() []runtime_pubsub.Subscription {
	var subs []runtime_pubsub.Subscription

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		subs = runtime_pubsub.DeclarativeKubernetes(a.operatorClient, log)
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

	var topicRoutes map[string]TopicRoute = make(map[string]TopicRoute)

	if a.appChannel == nil {
		return topicRoutes, nil
	}

	var subscriptions []runtime_pubsub.Subscription

	// handle app subscriptions
	if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		subscriptions = runtime_pubsub.GetSubscriptionsHTTP(a.appChannel, log)
	} else if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		client := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
		subscriptions = runtime_pubsub.GetSubscriptionsGRPC(client, log)
	}

	// handle declarative subscriptions
	ds := a.getDeclarativeSubscriptions()
	for _, s := range ds {
		skip := false

		// don't register duplicate subscriptions
		for _, sub := range subscriptions {
			if sub.Route == s.Route && sub.PubsubName == s.PubsubName && sub.Topic == s.Topic {
				log.Warnf("two identical subscriptions found (sources: declarative, app endpoint). topic: %s, route: %s, pubsubname: %s",
					s.Topic, s.Route, s.PubsubName)
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
			topicRoutes[s.PubsubName] = TopicRoute{routes: make(map[string]string)}
		}

		topicRoutes[s.PubsubName].routes[s.Topic] = s.Route
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
	return topicRoutes, nil
}

func (a *DaprRuntime) initExporter(c components_v1alpha1.Component) error {
	exporter, err := a.exporterRegistry.Create(c.Spec.Type)
	if err != nil {
		log.Warnf("error creating exporter %s: %s", c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	properties := a.convertMetadataItemsToProperties(c.Spec.Metadata)

	err = exporter.Init(a.runtimeConfig.ID, a.hostAddress, exporters.Metadata{
		Properties: properties,
	})
	if err != nil {
		log.Warnf("error initializing exporter %s: %s", c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
	}
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	return nil
}

func (a *DaprRuntime) initPubSub(c components_v1alpha1.Component) error {
	pubSub, err := a.pubSubRegistry.Create(c.Spec.Type)
	if err != nil {
		log.Warnf("error creating pub sub %s: %s", c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	properties := a.convertMetadataItemsToProperties(c.Spec.Metadata)
	properties["consumerID"] = a.runtimeConfig.ID

	err = pubSub.Init(pubsub.Metadata{
		Properties: properties,
	})
	if err != nil {
		log.Warnf("error initializing pub sub %s: %s", c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
		return err
	}

	pubsubName := c.ObjectMeta.Name

	a.scopedSubscriptions[pubsubName] = scopes.GetScopedTopics(scopes.SubscriptionScopes, a.runtimeConfig.ID, properties)
	a.scopedPublishings[pubsubName] = scopes.GetScopedTopics(scopes.PublishingScopes, a.runtimeConfig.ID, properties)
	a.allowedTopics[pubsubName] = scopes.GetAllowedTopics(properties)
	if _, ok := a.pubSubs[pubsubName]; !ok {
		if err := a.beginPubSub(pubsubName, pubSub); err != nil {
			return err
		}
	}
	a.pubSubs[pubsubName] = pubSub
	diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)

	return nil
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (a *DaprRuntime) Publish(req *pubsub.PublishRequest) error {
	if _, ok := a.pubSubs[req.PubsubName]; !ok {
		return errors.New("pubsub not found")
	}

	if allowed := a.isPubSubOperationAllowed(req.PubsubName, req.Topic, a.scopedPublishings[req.PubsubName]); !allowed {
		return errors.Errorf("topic %s is not allowed for app id %s", req.Topic, a.runtimeConfig.ID)
	}

	return a.pubSubs[req.PubsubName].Publish(req)
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
	var resolverMetadata = nr.Metadata{}

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		resolver, err = a.nameResolutionRegistry.Create("kubernetes")
	case modes.StandaloneMode:
		resolver, err = a.nameResolutionRegistry.Create("mdns")
		// properties to register mDNS instances.
		resolverMetadata.Properties = map[string]string{
			nr.MDNSInstanceName:    a.runtimeConfig.ID,
			nr.MDNSInstanceAddress: a.hostAddress,
			nr.MDNSInstancePort:    strconv.Itoa(a.runtimeConfig.InternalGRPCPort),
		}
	default:
		return errors.Errorf("remote calls not supported for %s mode", string(a.runtimeConfig.Mode))
	}

	if err != nil {
		log.Warnf("error creating name resolution resolver %s: %s", a.runtimeConfig.Mode, err)
		return err
	}

	if err = resolver.Init(resolverMetadata); err != nil {
		log.Errorf("failed to initialize name resolution resolver %s: %s", a.runtimeConfig.Mode, err)
		return err
	}

	a.nameResolver = resolver

	log.Infof("Initialized name resolution to %s", a.runtimeConfig.Mode)
	return nil
}

func (a *DaprRuntime) publishMessageHTTP(msg *pubsub.NewMessage) error {
	subject := ""
	var cloudEvent pubsub.CloudEventsEnvelope
	err := a.json.Unmarshal(msg.Data, &cloudEvent)
	if err == nil {
		subject = cloudEvent.Subject
	}

	route := a.topicRoutes[cloudEvent.PubsubName].routes[msg.Topic]
	req := invokev1.NewInvokeMethodRequest(route)
	req.WithHTTPExtension(nethttp.MethodPost, "")
	req.WithRawData(msg.Data, pubsub.ContentType)

	// subject contains the correlationID which is passed span context
	sc, _ := diag.SpanContextFromW3CString(subject)
	spanName := fmt.Sprintf("pubsub/%s", msg.Topic)
	ctx, span := diag.StartInternalCallbackSpan(spanName, sc, a.globalConfig.Spec.TracingSpec)

	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		return errors.Wrap(err, "error from app channel while sending pub/sub event to app")
	}

	statusCode := int(resp.Status().Code)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	_, body := resp.RawData()

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse pubsub.AppResponse
		err := a.json.Unmarshal(body, &appResponse)
		if err != nil {
			log.Debugf("skipping status check due to error parsing result from pub/sub event %v", cloudEvent.ID)
			// Return no error so message does not get reprocessed.
			return nil
		}

		switch appResponse.Status {
		case pubsub.Success:
			return nil
		case pubsub.Retry:
			return errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent.ID)
		case pubsub.Drop:
			log.Warn("DROP status returned from app while processing pub/sub event %v", cloudEvent.ID)
			return nil
		}
		return errors.Errorf("unknown status returned from app while processing pub/sub event %v: %v", cloudEvent.ID, appResponse.Status)
	}

	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent.ID, body, statusCode)
		return nil
	}

	// Every error from now on is a retriable error.
	log.Warnf("retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent.ID, body, statusCode)
	return errors.Errorf("retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent.ID, body, statusCode)
}

func (a *DaprRuntime) publishMessageGRPC(msg *pubsub.NewMessage) error {
	var cloudEvent pubsub.CloudEventsEnvelope
	err := a.json.Unmarshal(msg.Data, &cloudEvent)
	if err != nil {
		log.Debugf("error deserializing cloud events proto: %s", err)
		return err
	}

	envelope := &runtimev1pb.TopicEventRequest{
		Id:              cloudEvent.ID,
		Source:          cloudEvent.Source,
		DataContentType: cloudEvent.DataContentType,
		Type:            cloudEvent.Type,
		SpecVersion:     cloudEvent.SpecVersion,
		Topic:           msg.Topic,
		PubsubName:      cloudEvent.PubsubName,
	}

	if cloudEvent.Data != nil {
		envelope.Data = nil
		if cloudEvent.DataContentType == "text/plain" {
			envelope.Data = []byte(cloudEvent.Data.(string))
		} else if cloudEvent.DataContentType == "application/json" {
			envelope.Data, _ = a.json.Marshal(cloudEvent.Data)
		}
	}

	// subject contains the correlationID which is passed span context
	subject := cloudEvent.Subject
	sc, _ := diag.SpanContextFromW3CString(subject)
	spanName := fmt.Sprintf("pubsub/%s", msg.Topic)

	// no ops if trace is off
	ctx, span := diag.StartInternalCallbackSpan(spanName, sc, a.globalConfig.Spec.TracingSpec)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	// call appcallback
	clientV1 := runtimev1pb.NewAppCallbackClient(a.grpc.AppClient)
	res, err := clientV1.OnTopicEvent(ctx, envelope)

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
			log.Warn("non-retriable error returned from app while processing pub/sub event %v: %s", cloudEvent.ID, err)
			return nil
		}

		err = errors.Errorf("error returned from app while processing pub/sub event %v: %s", cloudEvent.ID, err)
		log.Debug(err)
	}

	switch res.GetStatus() {
	case runtimev1pb.TopicEventResponse_SUCCESS:
		return nil
	case runtimev1pb.TopicEventResponse_RETRY:
		return errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent.ID)
	case runtimev1pb.TopicEventResponse_DROP:
		log.Warn("DROP status returned from app while processing pub/sub event %v", cloudEvent.ID)
		return nil
	}

	return err
}

func (a *DaprRuntime) initActors() error {
	actorConfig := actors.NewConfig(a.hostAddress, a.runtimeConfig.ID, a.runtimeConfig.PlacementServiceAddress, a.appConfig.Entities,
		a.runtimeConfig.InternalGRPCPort, a.appConfig.ActorScanInterval, a.appConfig.ActorIdleTimeout, a.appConfig.DrainOngoingCallTimeout, a.appConfig.DrainRebalancedActors)
	act := actors.NewActors(a.stateStores[a.actorStateStoreName], a.appChannel, a.grpc.GetGRPCConnection, actorConfig, a.runtimeConfig.CertChain, a.globalConfig.Spec.TracingSpec)
	err := act.Init()
	a.actor = act
	return err
}

func (a *DaprRuntime) getAuthorizedComponents(components []components_v1alpha1.Component) []components_v1alpha1.Component {
	authorized := []components_v1alpha1.Component{}

	for _, c := range components {
		if a.namespace == "" || (a.namespace != "" && c.ObjectMeta.Namespace == a.namespace) {
			// scopes are defined, make sure this runtime ID is authorized
			if len(c.Scopes) > 0 {
				found := false
				for _, s := range c.Scopes {
					if s == a.runtimeConfig.ID {
						found = true
						break
					}
				}

				if !found {
					continue
				}
			}
			authorized = append(authorized, c)
		}
	}
	return authorized
}

func (a *DaprRuntime) loadComponents(opts *runtimeOpts) error {
	var loader components.ComponentLoader

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		loader = components.NewKubernetesComponents(a.runtimeConfig.Kubernetes, a.operatorClient)
	case modes.StandaloneMode:
		loader = components.NewStandaloneComponents(a.runtimeConfig.Standalone)
	default:
		return errors.Errorf("components loader for mode %s not found", a.runtimeConfig.Mode)
	}

	comps, err := loader.LoadComponents()
	if err != nil {
		return err
	}

	a.components = a.getAuthorizedComponents(comps)
	for _, comp := range a.components {
		a.pendingComponents <- comp
	}

	return nil
}

func (a *DaprRuntime) appendOrReplaceComponents(component components_v1alpha1.Component) {
	existed := a.getComponent(component.Spec.Type, component.Name)
	if existed == nil {
		a.components = append(a.components, component)
	} else {
		*existed = component
	}
}

func (a *DaprRuntime) figureOutComponentCategory(component components_v1alpha1.Component) ComponentCategory {
	for _, category := range componentCategoriesNeedProcess {
		if strings.HasPrefix(component.Spec.Type, fmt.Sprintf("%s.", category)) {
			return category
		}
	}
	return ""
}

func (a *DaprRuntime) processComponents() {
	for {
		comp, more := <-a.pendingComponents
		if !more {
			a.pendingComponentsDone <- true
			return
		}
		if err := a.processOneComponent(comp); err != nil {
			log.Errorf("process component %s error, %s", comp.Name, err)
		}
	}
}

func (a *DaprRuntime) flushOutstandingComponents() {
	// We flush by stopping the processComponents goroutine, waiting for it to finish, and then restart it
	log.Info("waiting for all outstanding components to be processed")
	close(a.pendingComponents)

	<-a.pendingComponentsDone
	log.Info("all outstanding components processed")

	a.pendingComponents = make(chan components_v1alpha1.Component)
	go a.processComponents()
}

func (a *DaprRuntime) processOneComponent(comp components_v1alpha1.Component) error {
	res := a.preprocessOneComponent(&comp)
	if res.unreadyDependency != "" {
		a.pendingComponentDependents[res.unreadyDependency] = append(a.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := a.figureOutComponentCategory(comp)
	if err := a.doProcessOneComponent(compCategory, comp); err != nil {
		return err
	}
	dependency := componentDependency(compCategory, comp.Name)
	if deps, ok := a.pendingComponentDependents[dependency]; ok {
		delete(a.pendingComponentDependents, dependency)
		go func(dependents []components_v1alpha1.Component) {
			for _, dependent := range dependents {
				a.pendingComponents <- dependent
			}
		}(deps)
	}

	a.appendOrReplaceComponents(comp)
	diag.DefaultMonitoring.ComponentLoaded()
	log.Infof("component loaded. name: %s, type: %s", comp.ObjectMeta.Name, comp.Spec.Type)
	return nil
}

func (a *DaprRuntime) doProcessOneComponent(category ComponentCategory, comp components_v1alpha1.Component) error {
	switch category {
	case bindingsComponent:
		return a.initBinding(comp)
	case exporterComponent:
		return a.initExporter(comp)
	case pubsubComponent:
		return a.initPubSub(comp)
	case secretStoreComponent:
		return a.initSecretStore(comp)
	case stateComponent:
		return a.initState(comp)
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

// Stop allows for a graceful shutdown of all runtime internal operations or components
func (a *DaprRuntime) Stop() {
	log.Info("stop command issued. Shutting down all operations")
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
			return component, secretStoreName
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
			component.Spec.Metadata[i].Value = val
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
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("localhost", fmt.Sprintf("%v", a.runtimeConfig.ApplicationPort)), time.Millisecond*500)
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

	var appConfigGetFn func() (*config.ApplicationConfig, error)

	switch a.runtimeConfig.ApplicationProtocol {
	case HTTPProtocol:
		appConfigGetFn = a.getConfigurationHTTP
	case GRPCProtocol:
		appConfigGetFn = a.getConfigurationGRPC
	default:
		appConfigGetFn = a.getConfigurationHTTP
	}

	appConfig, err := appConfigGetFn()
	if err != nil {
		return
	}

	if appConfig != nil {
		a.appConfig = *appConfig
		log.Info("application configuration loaded")
	}
}

// getConfigurationHTTP gets application config from user application
// GET http://localhost:<app_port>/dapr/config
func (a *DaprRuntime) getConfigurationHTTP() (*config.ApplicationConfig, error) {
	req := invokev1.NewInvokeMethodRequest(appConfigEndpoint)
	req.WithHTTPExtension(nethttp.MethodGet, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate context
	ctx := context.Background()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		return nil, err
	}

	var config config.ApplicationConfig

	if resp.Status().Code != nethttp.StatusOK {
		return &config, nil
	}

	contentType, body := resp.RawData()
	if contentType != invokev1.JSONContentType {
		log.Debugf("dapr/config returns invalid content_type: %s", contentType)
	}

	if err = a.json.Unmarshal(body, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (a *DaprRuntime) getConfigurationGRPC() (*config.ApplicationConfig, error) {
	return nil, nil
}

func (a *DaprRuntime) createAppChannel() error {
	if a.runtimeConfig.ApplicationPort > 0 {
		var channelCreatorFn func(port, maxConcurrency int, spec config.TracingSpec) (channel.AppChannel, error)

		switch a.runtimeConfig.ApplicationProtocol {
		case GRPCProtocol:
			channelCreatorFn = a.grpc.CreateLocalChannel
		case HTTPProtocol:
			channelCreatorFn = http_channel.CreateLocalChannel
		default:
			return errors.Errorf("cannot create app channel for protocol %s", string(a.runtimeConfig.ApplicationProtocol))
		}

		ch, err := channelCreatorFn(a.runtimeConfig.ApplicationPort, a.runtimeConfig.MaxConcurrency, a.globalConfig.Spec.TracingSpec)
		if err != nil {
			return err
		}
		if a.runtimeConfig.MaxConcurrency > 0 {
			log.Infof("app max concurrency set to %v", a.runtimeConfig.MaxConcurrency)
		}
		a.appChannel = ch
	}

	return nil
}

func (a *DaprRuntime) appendBuiltinSecretStore() {
	for _, comp := range a.builtinSecretStore() {
		a.pendingComponents <- comp
	}
}

func (a *DaprRuntime) builtinSecretStore() []components_v1alpha1.Component {
	// Preload Kubernetes secretstore
	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		return []components_v1alpha1.Component{{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetes",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetes",
			},
		}}
	}
	return nil
}

func (a *DaprRuntime) initSecretStore(c components_v1alpha1.Component) error {
	secretStore, err := a.secretStoresRegistry.Create(c.Spec.Type)
	if err != nil {
		log.Warnf("failed creating state store %s: %s", c.Spec.Type, err)
		diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
		return err
	}

	err = secretStore.Init(secretstores.Metadata{
		Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
	})
	if err != nil {
		log.Warnf("failed to init state store %s named %s: %s", c.Spec.Type, c.ObjectMeta.Name, err)
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
		properties[c.Name] = c.Value
	}
	return properties
}

func (a *DaprRuntime) getComponent(componentType string, name string) *components_v1alpha1.Component {
	for i, c := range a.components {
		if c.Spec.Type == componentType && c.ObjectMeta.Name == name {
			return &a.components[i]
		}
	}
	return nil
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
