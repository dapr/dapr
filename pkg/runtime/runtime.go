// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	http_channel "github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/components"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"
	exporter_loader "github.com/dapr/dapr/pkg/components/exporters"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	servicediscovery_loader "github.com/dapr/dapr/pkg/components/servicediscovery"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/discovery"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/http"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/messaging"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	daprclient_pb "github.com/dapr/dapr/pkg/proto/daprclient"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	jsoniter "github.com/json-iterator/go"
)

const (
	appConfigEndpoint   = "dapr/config"
	parallelConcurrency = "parallel"
	actorStateStore     = "actorStateStore"
)

var log = logger.NewLogger("dapr.runtime")

// DaprRuntime holds all the core components of the runtime
type DaprRuntime struct {
	runtimeConfig            *Config
	globalConfig             *config.Configuration
	components               []components_v1alpha1.Component
	grpc                     *grpc.Manager
	appChannel               channel.AppChannel
	appConfig                config.ApplicationConfig
	directMessaging          messaging.DirectMessaging
	stateStoreRegistry       state_loader.Registry
	secretStoresRegistry     secretstores_loader.Registry
	exporterRegistry         exporter_loader.Registry
	serviceDiscoveryRegistry servicediscovery_loader.Registry
	stateStores              map[string]state.Store
	actor                    actors.Actors
	bindingsRegistry         bindings_loader.Registry
	inputBindings            map[string]bindings.InputBinding
	outputBindings           map[string]bindings.OutputBinding
	secretStores             map[string]secretstores.SecretStore
	pubSubRegistry           pubsub_loader.Registry
	pubSub                   pubsub.PubSub
	servicediscoveryResolver servicediscovery.Resolver
	json                     jsoniter.API
	httpMiddlewareRegistry   http_middleware_loader.Registry
	hostAddress              string
	actorStateStoreName      string
	actorStateStoreCount     int
	authenticator            security.Authenticator
	namespace                string
	scopedPublishings        []string
	allowedTopics            []string
	daprHTTPAPI              http.API
	operatorClient           operatorv1pb.OperatorClient
}

// NewDaprRuntime returns a new runtime with the given runtime config and global config
func NewDaprRuntime(runtimeConfig *Config, globalConfig *config.Configuration) *DaprRuntime {
	return &DaprRuntime{
		runtimeConfig:            runtimeConfig,
		globalConfig:             globalConfig,
		grpc:                     grpc.NewGRPCManager(),
		json:                     jsoniter.ConfigFastest,
		inputBindings:            map[string]bindings.InputBinding{},
		outputBindings:           map[string]bindings.OutputBinding{},
		secretStores:             map[string]secretstores.SecretStore{},
		stateStores:              map[string]state.Store{},
		stateStoreRegistry:       state_loader.NewRegistry(),
		bindingsRegistry:         bindings_loader.NewRegistry(),
		pubSubRegistry:           pubsub_loader.NewRegistry(),
		secretStoresRegistry:     secretstores_loader.NewRegistry(),
		exporterRegistry:         exporter_loader.NewRegistry(),
		serviceDiscoveryRegistry: servicediscovery_loader.NewRegistry(),
		httpMiddlewareRegistry:   http_middleware_loader.NewRegistry(),
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

	err = a.beginReadInputBindings()
	if err != nil {
		log.Warn(err)
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
			return nil, fmt.Errorf("error creating operator client: %s", err)
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

	err = a.loadComponents(opts)
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}
	err = a.beginComponentsUpdates()
	if err != nil {
		log.Warnf("failed to watch component updates: %s", err)
	}

	a.blockUntilAppIsReady()

	a.hostAddress, err = GetHostAddress()
	if err != nil {
		return fmt.Errorf("failed to determine host address: %s", err)
	}

	err = a.createAppChannel()
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.ApplicationProtocol), err)
	}

	a.loadAppConfiguration()

	// Register and initialize state stores
	a.stateStoreRegistry.Register(opts.states...)
	err = a.initState(a.stateStoreRegistry)
	if err != nil {
		log.Warnf("failed to init state: %s", err)
	}

	// Register and initialize pub/sub
	a.pubSubRegistry.Register(opts.pubsubs...)
	err = a.initPubSub()
	if err != nil {
		log.Warnf("failed to init pubsub: %s", err)
	}

	// Register and initialize exporters
	a.exporterRegistry.Register(opts.exporters...)
	err = a.initExporters()
	if err != nil {
		log.Warnf("failed to init exporters: %s", err)
	}

	// Register and initialize service discovery
	a.serviceDiscoveryRegistry.Register(opts.serviceDiscovery...)
	err = a.initServiceDiscovery()
	if err != nil {
		log.Warnf("failed to init service discovery: %s", err)
	}

	// Register and initialize bindings
	a.bindingsRegistry.RegisterInputBindings(opts.inputBindings...)
	a.bindingsRegistry.RegisterOutputBindings(opts.outputBindings...)
	a.initBindings()
	a.initDirectMessaging(a.servicediscoveryResolver)

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

	// Announce presence to local network if self-hosted
	err = a.announceSelf()
	if err != nil {
		log.Warnf("failed to broadcast address to local network: %s", err)
	}

	return nil
}

func (a *DaprRuntime) buildHTTPPipeline() (http_middleware.Pipeline, error) {
	var handlers []http_middleware.Middleware

	if a.globalConfig != nil {
		for i := 0; i < len(a.globalConfig.Spec.HTTPPipelineSpec.Handlers); i++ {
			middlewareSpec := a.globalConfig.Spec.HTTPPipelineSpec.Handlers[i]
			component := a.getComponent(middlewareSpec.Type, middlewareSpec.Name)
			if component == nil {
				return http_middleware.Pipeline{}, fmt.Errorf("couldn't find middleware component with name %s and type %s",
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

func (a *DaprRuntime) initBindings() {
	err := a.initOutputBindings(a.bindingsRegistry)
	if err != nil {
		log.Errorf("failed to init output bindings: %s", err)
	}

	err = a.initInputBindings(a.bindingsRegistry)
	if err != nil {
		log.Errorf("failed to init input bindings: %s", err)
	}
}

func (a *DaprRuntime) beginReadInputBindings() error {
	for key, b := range a.inputBindings {
		go func(name string, binding bindings.InputBinding) {
			err := a.readFromBinding(name, binding)
			if err != nil {
				log.Errorf("error reading from input binding %s: %s", name, err)
			}
		}(key, b)
	}

	return nil
}

func (a *DaprRuntime) initDirectMessaging(resolver servicediscovery.Resolver) {
	a.directMessaging = messaging.NewDirectMessaging(
		a.runtimeConfig.ID,
		a.namespace,
		a.runtimeConfig.InternalGRPCPort,
		a.runtimeConfig.Mode,
		a.appChannel,
		a.grpc.GetGRPCConnection,
		resolver)
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
			err = json.Unmarshal(c.Component.Value, &component)
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
	update := false

	for i, c := range a.components {
		if c.Spec.Type == component.Spec.Type && c.ObjectMeta.Name == component.ObjectMeta.Name {
			if reflect.DeepEqual(c.Spec.Metadata, component.Spec.Metadata) {
				return
			}

			a.components[i] = component
			update = true
			break
		}
	}

	if !update {
		a.components = append(a.components, component)
	}

	if strings.Index(component.Spec.Type, "state") == 0 {
		store, err := a.stateStoreRegistry.CreateStateStore(component.Spec.Type)
		if err != nil {
			log.Errorf("error creating state store: %s", err)
			return
		}

		err = store.Init(state.Metadata{
			Properties: a.convertMetadataItemsToProperties(component.Spec.Metadata),
		})
		if err != nil {
			log.Errorf("error on init state store: %s", err)
		} else {
			a.stateStores[component.ObjectMeta.Name] = store
		}
	} else if strings.Index(component.Spec.Type, "bindings") == 0 {
		//TODO: implement update for input bindings too
		binding, err := a.bindingsRegistry.CreateOutputBinding(component.Spec.Type)
		if err != nil {
			log.Errorf("failed to create output binding: %s", err)
			return
		}

		err = binding.Init(bindings.Metadata{
			Properties: a.convertMetadataItemsToProperties(component.Spec.Metadata),
			Name:       component.ObjectMeta.Name,
		})
		if err == nil {
			a.outputBindings[component.ObjectMeta.Name] = binding
		}
	}
}

func (a *DaprRuntime) sendBatchOutputBindingsParallel(to []string, data []byte) {
	for _, dst := range to {
		go func(name string) {
			err := a.sendToOutputBinding(name, &bindings.WriteRequest{
				Data: data,
			})
			if err != nil {
				log.Error(err)
			}
		}(dst)
	}
}

func (a *DaprRuntime) sendBatchOutputBindingsSequential(to []string, data []byte) error {
	for _, dst := range to {
		err := a.sendToOutputBinding(dst, &bindings.WriteRequest{
			Data: data,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *DaprRuntime) sendToOutputBinding(name string, req *bindings.WriteRequest) error {
	if binding, ok := a.outputBindings[name]; ok {
		err := binding.Write(req)
		return err
	}
	return fmt.Errorf("couldn't find output binding %s", name)
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

		if response.Concurrency == parallelConcurrency {
			a.sendBatchOutputBindingsParallel(response.To, b)
		} else {
			return a.sendBatchOutputBindingsSequential(response.To, b)
		}
	}

	return nil
}

func (a *DaprRuntime) sendBindingEventToApp(bindingName string, data []byte, metadata map[string]string) error {
	var response *bindings.AppResponse

	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		client := daprclient_pb.NewDaprClientClient(a.grpc.AppClient)
		resp, err := client.OnBindingEvent(context.Background(), &daprclient_pb.BindingEventEnvelope{
			Name: bindingName,
			Data: &any.Any{
				Value: data,
			},
			Metadata: metadata,
		})
		if err != nil {
			return fmt.Errorf("error invoking app: %s", err)
		}
		if resp != nil {
			response = &bindings.AppResponse{}
			response.Concurrency = resp.Concurrency
			response.To = resp.To

			if resp.Data != nil {
				var d interface{}
				err := a.json.Unmarshal(resp.Data.Value, &d)
				if err == nil {
					response.Data = d
				}
			}

			for _, s := range resp.State {
				var i interface{}
				a.json.Unmarshal(s.Value.Value, &i)

				response.State = append(response.State, state.SetRequest{
					Key:   s.Key,
					Value: i,
				})
			}
		}
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		req := channel.InvokeRequest{
			Metadata: metadata,
			Method:   bindingName,
			Payload:  data,
		}

		resp, err := a.appChannel.InvokeMethod(&req)
		if err != nil || http.GetStatusCodeFromMetadata(resp.Metadata) != 200 {
			return fmt.Errorf("error invoking app: %s", err)
		}

		if resp != nil {
			var r bindings.AppResponse
			err := a.json.Unmarshal(resp.Data, &r)
			if err != nil {
				log.Debugf("error deserializing app response: %s", err)
			} else {
				response = &r
			}
		}
	}

	if response != nil {
		err := a.onAppResponse(response)
		if err != nil {
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
	a.daprHTTPAPI = http.NewAPI(a.runtimeConfig.ID, a.appChannel, a.directMessaging, a.stateStores, a.secretStores, a.getPublishAdapter(), a.actor, a.sendToOutputBinding)
	serverConf := http.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port, profilePort, allowedOrigins, a.runtimeConfig.EnableProfiling)

	server := http.NewServer(a.daprHTTPAPI, serverConf, a.globalConfig.Spec.TracingSpec, pipeline)
	server.StartNonBlocking()
}

func (a *DaprRuntime) startGRPCInternalServer(api grpc.API, port int) error {
	serverConf := grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port)
	server := grpc.NewInternalServer(api, serverConf, a.globalConfig.Spec.TracingSpec, a.authenticator)
	err := server.StartNonBlocking()
	return err
}

func (a *DaprRuntime) startGRPCAPIServer(api grpc.API, port int) error {
	serverConf := grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port)
	server := grpc.NewAPIServer(api, serverConf, a.globalConfig.Spec.TracingSpec)
	err := server.StartNonBlocking()
	return err
}

func (a *DaprRuntime) getGRPCAPI() grpc.API {
	return grpc.NewAPI(a.runtimeConfig.ID, a.appChannel, a.stateStores, a.secretStores, a.getPublishAdapter(), a.directMessaging, a.actor, a.sendToOutputBinding)
}

func (a *DaprRuntime) getPublishAdapter() func(*pubsub.PublishRequest) error {
	if a.pubSub == nil {
		return nil
	}
	return a.Publish
}

func (a *DaprRuntime) getSubscribedBindingsGRPC() []string {
	client := daprclient_pb.NewDaprClientClient(a.grpc.AppClient)
	resp, err := client.GetBindingsSubscriptions(context.Background(), &empty.Empty{})
	bindings := []string{}

	if err == nil && resp != nil {
		bindings = resp.Bindings
	}
	return bindings
}

func (a *DaprRuntime) isAppSubscribedToBinding(binding string, bindingsList []string) bool {
	// if gRPC, looks for the binding in the list of bindings returned from the app
	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		for _, b := range bindingsList {
			if b == binding {
				return true
			}
		}
	} else if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		// if HTTP, check if there's an endpoint listening for that binding
		req := channel.InvokeRequest{
			Method:   binding,
			Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Options},
		}
		resp, err := a.appChannel.InvokeMethod(&req)
		if err == nil && resp != nil {
			statusCode := http.GetStatusCodeFromMetadata(resp.Metadata)
			return statusCode != 404
		}
	}
	return false
}

func (a *DaprRuntime) initInputBindings(registry bindings_loader.Registry) error {
	if a.appChannel == nil {
		return fmt.Errorf("app channel not initialized")
	}

	bindingsList := []string{}
	if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		bindingsList = a.getSubscribedBindingsGRPC()
	}

	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "bindings") == 0 {
			subscribed := a.isAppSubscribedToBinding(c.ObjectMeta.Name, bindingsList)
			if !subscribed {
				continue
			}

			binding, err := registry.CreateInputBinding(c.Spec.Type)
			if err != nil {
				log.Errorf("failed to create input binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
				continue
			}
			err = binding.Init(bindings.Metadata{
				Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
				Name:       c.ObjectMeta.Name,
			})
			if err != nil {
				log.Errorf("failed to init input binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
				continue
			}

			log.Infof("successful init for input binding %s (%s)", c.ObjectMeta.Name, c.Spec.Type)
			a.inputBindings[c.ObjectMeta.Name] = binding
			diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
		}
	}
	return nil
}

func (a *DaprRuntime) initOutputBindings(registry bindings_loader.Registry) error {
	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "bindings") == 0 {
			binding, err := registry.CreateOutputBinding(c.Spec.Type)
			if err != nil {
				log.Errorf("failed to create output binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
				continue
			}

			if binding != nil {
				err := binding.Init(bindings.Metadata{
					Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
					Name:       c.ObjectMeta.Name,
				})
				if err != nil {
					log.Errorf("failed to init output binding %s (%s): %s", c.ObjectMeta.Name, c.Spec.Type, err)
					diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
					continue
				}
				log.Infof("successful init for output binding %s (%s)", c.ObjectMeta.Name, c.Spec.Type)
				a.outputBindings[c.ObjectMeta.Name] = binding
				diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
			}
		}
	}
	return nil
}

// Refer for state store api decision  https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (a *DaprRuntime) initState(registry state_loader.Registry) error {
	for _, s := range a.components {
		if strings.Index(s.Spec.Type, "state") == 0 {
			store, err := registry.CreateStateStore(s.Spec.Type)
			if err != nil {
				log.Warnf("error creating state store %s: %s", s.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "creation")
				continue
			}
			if store != nil {
				props := a.convertMetadataItemsToProperties(s.Spec.Metadata)
				err := store.Init(state.Metadata{
					Properties: props,
				})
				if err != nil {
					diag.DefaultMonitoring.ComponentInitFailed(s.Spec.Type, "init")
					log.Warnf("error initializing state store %s: %s", s.Spec.Type, err)
					continue
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
		}
	}

	if a.actorStateStoreName == "" || a.actorStateStoreCount != 1 {
		log.Warnf("either no actor state store or multiple actor state stores are specified in the configuration, actor stores specified: %d", a.actorStateStoreCount)
	}

	return nil
}

func (a *DaprRuntime) getSubscribedTopicsFromApp() []string {
	topics := []string{}
	if a.appChannel == nil {
		return topics
	}

	if a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
		req := &channel.InvokeRequest{
			Method:   "dapr/subscribe",
			Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
		}

		resp, err := a.appChannel.InvokeMethod(req)
		statusCode := http.GetStatusCodeFromMetadata(resp.Metadata)
		if err == nil && statusCode == 200 {
			err := json.Unmarshal(resp.Data, &topics)
			if err != nil {
				log.Errorf("error getting topics from app: %s", err)
			}
		} else if statusCode != 200 && statusCode != 404 {
			log.Warnf("app returned http status code %v from subscription endpoint", statusCode)
		}
	} else if a.runtimeConfig.ApplicationProtocol == GRPCProtocol {
		client := daprclient_pb.NewDaprClientClient(a.grpc.AppClient)
		resp, err := client.GetTopicSubscriptions(context.Background(), &empty.Empty{})
		if err == nil && resp != nil {
			topics = resp.Topics
		}
	}

	log.Infof("app is subscribed to the following topics: %v", topics)
	return topics
}

func (a *DaprRuntime) initExporters() error {
	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "exporter") == 0 {
			exporter, err := a.exporterRegistry.Create(c.Spec.Type)
			if err != nil {
				log.Warnf("error creating exporter %s: %s", c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
				continue
			}

			properties := a.convertMetadataItemsToProperties(c.Spec.Metadata)

			err = exporter.Init(a.runtimeConfig.ID, a.hostAddress, exporters.Metadata{
				Properties: properties,
			})
			if err != nil {
				log.Warnf("error initializing exporter %s: %s", c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
				continue
			}
			diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
		}
	}
	return nil
}

func (a *DaprRuntime) initPubSub() error {
	var scopedSubscriptions []string

	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "pubsub") == 0 {
			pubSub, err := a.pubSubRegistry.Create(c.Spec.Type)
			if err != nil {
				log.Warnf("error creating pub sub %s: %s", c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
				continue
			}

			properties := a.convertMetadataItemsToProperties(c.Spec.Metadata)
			properties["consumerID"] = a.runtimeConfig.ID

			err = pubSub.Init(pubsub.Metadata{
				Properties: properties,
			})
			if err != nil {
				log.Warnf("error initializing pub sub %s: %s", c.Spec.Type, err)
				diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
				continue
			}

			scopedSubscriptions = scopes.GetScopedTopics(scopes.SubscriptionScopes, a.runtimeConfig.ID, properties)
			a.scopedPublishings = scopes.GetScopedTopics(scopes.PublishingScopes, a.runtimeConfig.ID, properties)
			a.allowedTopics = scopes.GetAllowedTopics(properties)

			a.pubSub = pubSub
			diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
			break
		}
	}

	var publishFunc func(msg *pubsub.NewMessage) error
	switch a.runtimeConfig.ApplicationProtocol {
	case HTTPProtocol:
		publishFunc = a.publishMessageHTTP
	case GRPCProtocol:
		publishFunc = a.publishMessageGRPC
	}

	if a.pubSub != nil && a.appChannel != nil {
		topics := a.getSubscribedTopicsFromApp()
		for _, t := range topics {
			allowed := a.isPubSubOperationAllowed(t, scopedSubscriptions)
			if !allowed {
				log.Warnf("subscription to topic %s is not allowed", t)
				continue
			}

			err := a.pubSub.Subscribe(pubsub.SubscribeRequest{
				Topic: t,
			}, publishFunc)
			if err != nil {
				log.Warnf("failed to subscribe to topic %s: %s", t, err)
			}
		}
	}
	return nil
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (a *DaprRuntime) Publish(req *pubsub.PublishRequest) error {
	if allowed := a.isPubSubOperationAllowed(req.Topic, a.scopedPublishings); !allowed {
		return fmt.Errorf("topic %s is not allowed for app id %s", req.Topic, a.runtimeConfig.ID)
	}
	return a.pubSub.Publish(req)
}

func (a *DaprRuntime) isPubSubOperationAllowed(topic string, scopedTopics []string) bool {
	inAllowedTopics := false

	// first check if allowedTopics contain it
	if len(a.allowedTopics) > 0 {
		for _, t := range a.allowedTopics {
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

func (a *DaprRuntime) initServiceDiscovery() error {
	var resolver servicediscovery.Resolver
	var err error
	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		resolver, err = a.serviceDiscoveryRegistry.Create("kubernetes")
	case modes.StandaloneMode:
		resolver, err = a.serviceDiscoveryRegistry.Create("mdns")

	default:
		return fmt.Errorf("remote calls not supported for %s mode", string(a.runtimeConfig.Mode))
	}

	if err != nil {
		log.Warnf("error creating service discovery resolver %s: %s", a.runtimeConfig.Mode, err)
		return err
	}

	a.servicediscoveryResolver = resolver

	log.Infof("Initialized service discovery to %s", a.runtimeConfig.Mode)
	return nil
}

func (a *DaprRuntime) publishMessageHTTP(msg *pubsub.NewMessage) error {
	subject := ""
	var cloudEvent pubsub.CloudEventsEnvelope
	err := a.json.Unmarshal(msg.Data, &cloudEvent)
	if err == nil {
		subject = cloudEvent.Subject
	}

	req := channel.InvokeRequest{
		Method:  msg.Topic,
		Payload: msg.Data,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Post,
			"headers":          fmt.Sprintf("%s%s%s", http_channel.ContentType, http_channel.HeaderEquals, pubsub.ContentType),
			diag.CorrelationID: subject},
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	statusCode := 0

	if resp != nil {
		statusCode = http.GetStatusCodeFromMetadata(resp.Metadata)
	}

	if err != nil || statusCode != 200 {
		var err error
		if resp == nil {
			err = fmt.Errorf("error from app channel while sending pub/sub event to app: %s", err)
		} else {
			err = fmt.Errorf("error returned from app while processing pub/sub event: %s. status code returned: %v", string(resp.Data), statusCode)
		}

		log.Debug(err)
		return err
	}
	return nil
}

func (a *DaprRuntime) publishMessageGRPC(msg *pubsub.NewMessage) error {
	var cloudEvent pubsub.CloudEventsEnvelope
	err := a.json.Unmarshal(msg.Data, &cloudEvent)
	if err != nil {
		log.Debugf("error deserializing cloud events proto: %s", err)
		return err
	}

	envelope := &daprclient_pb.CloudEventEnvelope{
		Id:              cloudEvent.ID,
		Source:          cloudEvent.Source,
		DataContentType: cloudEvent.DataContentType,
		Type:            cloudEvent.Type,
		SpecVersion:     cloudEvent.SpecVersion,
		Topic:           msg.Topic,
	}

	if cloudEvent.Data != nil {
		var b []byte
		if cloudEvent.DataContentType == "text/plain" {
			b = []byte(cloudEvent.Data.(string))
		} else if cloudEvent.DataContentType == "application/json" {
			b, _ = a.json.Marshal(cloudEvent.Data)
		}
		envelope.Data = &any.Any{
			Value: b,
		}
	}

	client := daprclient_pb.NewDaprClientClient(a.grpc.AppClient)
	_, err = client.OnTopicEvent(context.Background(), envelope)
	if err != nil {
		err = fmt.Errorf("error from app while processing pub/sub event: %s", err)
		log.Debug(err)
		return err
	}
	return nil
}

func (a *DaprRuntime) initActors() error {
	actorConfig := actors.NewConfig(a.hostAddress, a.runtimeConfig.ID, a.runtimeConfig.PlacementServiceAddress, a.appConfig.Entities,
		a.runtimeConfig.InternalGRPCPort, a.appConfig.ActorScanInterval, a.appConfig.ActorIdleTimeout, a.appConfig.DrainOngoingCallTimeout, a.appConfig.DrainRebalancedActors)
	act := actors.NewActors(a.stateStores[a.actorStateStoreName], a.appChannel, a.grpc.GetGRPCConnection, actorConfig, a.runtimeConfig.CertChain)
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
		return fmt.Errorf("components loader for mode %s not found", a.runtimeConfig.Mode)
	}

	comps, err := loader.LoadComponents()
	if err != nil {
		return err
	}
	a.components = a.getAuthorizedComponents(comps)

	// Register and initialize secret stores
	a.secretStoresRegistry.Register(opts.secretStores...)
	err = a.initSecretStores()
	if err != nil {
		log.Warnf("failed to init secret stores: %s", err)
	}

	var wg sync.WaitGroup
	wg.Add(len(a.components))

	for i, c := range a.components {
		go func(wg *sync.WaitGroup, component components_v1alpha1.Component, index int) {
			modified := a.processComponentSecrets(component)
			a.components[index] = modified
			log.Infof("found component %s (%s)", modified.ObjectMeta.Name, modified.Spec.Type)
			diag.DefaultMonitoring.ComponentLoaded()
			wg.Done()
		}(&wg, c, i)
	}
	wg.Wait()
	return nil
}

// Stop allows for a graceful shutdown of all runtime internal operations or components
func (a *DaprRuntime) Stop() {
	log.Info("stop command issued. Shutting down all operations")
}

func (a *DaprRuntime) processComponentSecrets(component components_v1alpha1.Component) components_v1alpha1.Component {
	cache := map[string]secretstores.GetSecretResponse{}

	for i, m := range component.Spec.Metadata {
		if m.SecretKeyRef.Name == "" {
			continue
		}

		secretStore := a.getSecretStore(component.Auth.SecretStore)
		if secretStore == nil {
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
			component.Spec.Metadata[i].Value = val
		}

		cache[m.SecretKeyRef.Name] = resp
	}
	return component
}

func (a *DaprRuntime) getSecretStore(storeName string) secretstores.SecretStore {
	if storeName == "" {
		switch a.runtimeConfig.Mode {
		case modes.KubernetesMode:
			storeName = "kubernetes"
		case modes.StandaloneMode:
			return nil
		}
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

func (a *DaprRuntime) getConfigurationHTTP() (*config.ApplicationConfig, error) {
	req := channel.InvokeRequest{
		Method:   appConfigEndpoint,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil {
		return nil, err
	}

	var config config.ApplicationConfig
	err = a.json.Unmarshal(resp.Data, &config)
	if err != nil {
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
			return fmt.Errorf("cannot create app channel for protocol %s", string(a.runtimeConfig.ApplicationProtocol))
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

func (a *DaprRuntime) announceSelf() error {
	switch a.runtimeConfig.Mode {
	case modes.StandaloneMode:
		err := discovery.RegisterMDNS(a.runtimeConfig.ID, a.runtimeConfig.InternalGRPCPort)
		if err != nil {
			return err
		}
		log.Info("local service entry announced")
	}
	return nil
}

func (a *DaprRuntime) initSecretStores() error {
	// Preload Kubernetes secretstore
	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		kubeSecretStore, err := a.secretStoresRegistry.Create("secretstores.kubernetes")
		if err != nil {
			return err
		}

		if err = kubeSecretStore.Init(secretstores.Metadata{}); err != nil {
			return err
		}

		a.secretStores["kubernetes"] = kubeSecretStore
	}

	// Initialize all secretstore components
	for _, c := range a.components {
		if !strings.Contains(c.Spec.Type, "secretstores") {
			continue
		}

		// Look up the secrets to authenticate this secretstore from K8S secret store
		a.processComponentSecrets(c)

		secretStore, err := a.secretStoresRegistry.Create(c.Spec.Type)
		if err != nil {
			log.Warnf("failed creating state store %s: %s", c.Spec.Type, err)
			diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "creation")
			continue
		}

		err = secretStore.Init(secretstores.Metadata{
			Properties: a.convertMetadataItemsToProperties(c.Spec.Metadata),
		})
		if err != nil {
			log.Warnf("failed to init state store %s named %s: %s", c.Spec.Type, c.ObjectMeta.Name, err)
			diag.DefaultMonitoring.ComponentInitFailed(c.Spec.Type, "init")
			continue
		}

		a.secretStores[c.ObjectMeta.Name] = secretStore
		diag.DefaultMonitoring.ComponentInitialized(c.Spec.Type)
	}

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
	for _, c := range a.components {
		if c.Spec.Type == componentType && c.ObjectMeta.Name == name {
			return &c
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
