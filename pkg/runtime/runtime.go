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
	"time"

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/actionscore/actions/pkg/components/state"
	"github.com/actionscore/actions/pkg/messaging"
	pubsub_loader "github.com/actionscore/actions/pkg/pubsub"
	state_loader "github.com/actionscore/actions/pkg/state"

	bindings_loader "github.com/actionscore/actions/pkg/bindings"
	"github.com/actionscore/actions/pkg/components/bindings"

	"github.com/actionscore/actions/pkg/http"
	jsoniter "github.com/json-iterator/go"

	"github.com/actionscore/actions/pkg/channel"
	http_channel "github.com/actionscore/actions/pkg/channel/http"

	"github.com/actionscore/actions/pkg/config"
	"github.com/actionscore/actions/pkg/grpc"
	"github.com/golang/protobuf/ptypes/empty"

	"github.com/actionscore/actions/pkg/components"
	"github.com/actionscore/actions/pkg/modes"

	log "github.com/Sirupsen/logrus"
	pb "github.com/actionscore/actions/pkg/proto"
)

const (
	appConfigEndpoint   = "actions/config"
	hostIPEnvVar        = "HOST_IP"
	parallelConcurrency = "parallel"
)

// ActionsRuntime holds all the core components of the runtime
type ActionsRuntime struct {
	runtimeConfig      *Config
	globalConfig       *config.Configuration
	components         []components.Component
	grpc               *grpc.Manager
	appChannel         channel.AppChannel
	appConfig          config.ApplicationConfig
	directMessaging    messaging.DirectMessaging
	stateStoreRegistry state.StateStoreRegistry
	stateStore         state.StateStore
	actor              actors.Actors
	bindingsRegistry   bindings.BindingsRegistry
	inputBindings      map[string]bindings.InputBinding
	outputBindings     map[string]bindings.OutputBinding
	pubSubRegistry     pubsub.PubSubRegistry
	pubSub             pubsub.PubSub
	json               jsoniter.API
	hostAddress        string
}

// NewActionsRuntime returns a new runtime with the given runtime config and global config
func NewActionsRuntime(runtimeConfig *Config, globalConfig *config.Configuration) *ActionsRuntime {
	return &ActionsRuntime{
		runtimeConfig:      runtimeConfig,
		globalConfig:       globalConfig,
		grpc:               grpc.NewGRPCManager(),
		json:               jsoniter.ConfigFastest,
		inputBindings:      map[string]bindings.InputBinding{},
		outputBindings:     map[string]bindings.OutputBinding{},
		stateStoreRegistry: state.NewStateStoreRegistry(),
		bindingsRegistry:   bindings.NewBindingsRegistry(),
		pubSubRegistry:     pubsub.NewPubSubRegsitry(),
	}
}

// Run performs initialization of the runtime with the runtime and global configurations
func (a *ActionsRuntime) Run() error {
	start := time.Now()
	log.Infof("%s mode configured", a.runtimeConfig.Mode)
	log.Infof("action id: %s", a.runtimeConfig.ID)

	err := a.initRuntime()
	if err != nil {
		return err
	}

	err = a.beginReadInputBindings()
	if err != nil {
		log.Warn(err)
	}

	d := time.Since(start).Seconds() * 1000
	log.Infof("actions initialized. Status: Running. Init Elapsed %vms", d)

	return nil
}

func (a *ActionsRuntime) initRuntime() error {
	err := a.loadComponents()
	if err != nil {
		log.Warnf("failed to load components: %s", err)
	}

	a.blockUntilAppIsReady()

	err = a.setHostAddress()
	if err != nil {
		log.Warnf("failed to set host address: %s", err)
	}

	err = a.createAppChannel()
	if err != nil {
		log.Warnf("failed to open %s channel to app: %s", string(a.runtimeConfig.ApplicationProtocol), err)
	}

	a.loadAppConfiguration()

	err = a.initState(a.stateStoreRegistry)
	if err != nil {
		log.Warnf("failed to init state: %s", err)
	}

	pubsub_loader.Load()
	err = a.initPubSub()
	if err != nil {
		log.Warnf("failed to init pubsub: %s", err)
	}

	a.initBindings()
	a.initDirectMessaging()

	err = a.initActors()
	if err != nil {
		log.Warnf("failed to init actors: %s", err)
	}

	a.startHTTPServer(a.runtimeConfig.HTTPPort, a.runtimeConfig.ProfilePort, a.runtimeConfig.AllowedOrigins)
	log.Infof("http server is running on port %v", a.runtimeConfig.HTTPPort)

	err = a.startGRPCServer(a.runtimeConfig.GRPCPort)
	if err != nil {
		log.Fatalf("failed to start gRPC server: %s", err)
	}
	log.Infof("gRPC server is running on port %v", a.runtimeConfig.GRPCPort)

	return nil
}

func (a *ActionsRuntime) initBindings() {
	bindings_loader.Load()
	err := a.initOutputBindings(a.bindingsRegistry)
	if err != nil {
		log.Warnf("failed to init output bindings: %s", err)
	}

	err = a.initInputBindings(a.bindingsRegistry)
	if err != nil {
		log.Warnf("failed to init input bindings: %s", err)
	}
}

func (a *ActionsRuntime) beginReadInputBindings() error {
	for key, b := range a.inputBindings {
		go func(name string, binding bindings.InputBinding) {
			err := a.readFromBinding(name, binding)
			if err != nil {
				log.Warnf("error reading from input binding: %s", err)
			}
		}(key, b)
	}

	return nil
}

func (a *ActionsRuntime) initDirectMessaging() {
	a.directMessaging = messaging.NewDirectMessaging(a.runtimeConfig.ID, os.Getenv("NAMESPACE"), a.runtimeConfig.GRPCPort, a.runtimeConfig.Mode, a.appChannel, a.grpc.GetGRPCConnection)
}

// OnComponentUpdated updates the Actions runtime with new or changed components
// This method is invoked from the Actions Control Plane whenever a component update happens
func (a *ActionsRuntime) OnComponentUpdated(component components.Component) {
	update := false

	for i, c := range a.components {
		if c.Spec.Type == component.Spec.Type && c.Metadata.Name == component.Metadata.Name {
			if reflect.DeepEqual(c, component) {
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
			ConnectionInfo: component.Spec.ConnectionInfo,
			Properties:     component.Spec.Properties,
		})
		if err != nil {
			log.Errorf("error on init state store: %s", err)
		} else {
			a.stateStore = store
		}
	} else if strings.Index(component.Spec.Type, "bindings") == 0 {
		//TODO: implement update for input bindings too
		binding, err := a.bindingsRegistry.CreateOutputBinding(component.Spec.Type)
		if err != nil {
			log.Errorf("Failed to create output binding: %s", err)
			return
		}

		err = binding.Init(bindings.Metadata{
			ConnectionInfo: component.Spec.ConnectionInfo,
			Properties:     component.Spec.Properties,
			Name:           component.Metadata.Name,
		})
		if err == nil {
			a.outputBindings[component.Metadata.Name] = binding
		}
	}
}

func (a *ActionsRuntime) sendBatchOutputBindingsParallel(to []string, data []byte) {
	for _, dst := range to {
		go func(name string) {
			err := a.sendToOutputBinding(name, data)
			if err != nil {
				log.Error(err)
			}
		}(dst)
	}
}

func (a *ActionsRuntime) sendBatchOutputBindingsSequential(to []string, data []byte) error {
	for _, dst := range to {
		err := a.sendToOutputBinding(dst, data)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *ActionsRuntime) sendToOutputBinding(name string, data []byte) error {
	if binding, ok := a.outputBindings[name]; ok {
		err := binding.Write(&bindings.WriteRequest{
			Data: data,
		})
		return err
	}

	return fmt.Errorf("couldn't find output binding %s", name)
}

func (a *ActionsRuntime) onAppResponse(response *bindings.AppResponse) error {
	if len(response.State) > 0 {
		go func(reqs []state.SetRequest) {
			if a.stateStore != nil {
				err := a.stateStore.BulkSet(reqs)
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

func (a *ActionsRuntime) readFromBinding(name string, binding bindings.InputBinding) error {
	err := binding.Read(func(resp *bindings.ReadResponse) error {
		if resp != nil {
			req := channel.InvokeRequest{
				Metadata: resp.Metadata,
				Method:   name,
				Payload:  resp.Data,
			}
			resp, err := a.appChannel.InvokeMethod(&req)
			if err != nil {
				return errors.New("error from app")
			}

			if resp != nil {
				statusCode := http.GetStatusCodeFromMetadata(resp.Metadata)
				if statusCode != 200 && statusCode != 201 {
					return errors.New("error from app")
				}

				var response bindings.AppResponse
				err := a.json.Unmarshal(resp.Data, &response)
				if err == nil {
					err = a.onAppResponse(&response)
					if err != nil {
						log.Errorf("error executing app response: %s", err)
					}
				}
			}
		}

		return nil
	})
	return err
}

func (a *ActionsRuntime) startHTTPServer(port, profilePort int, allowedOrigins string) {
	api := http.NewAPI(a.runtimeConfig.ID, a.appChannel, a.directMessaging, a.stateStore, a.pubSub, a.actor, a.sendToOutputBinding)
	serverConf := http.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port, profilePort, allowedOrigins)
	server := http.NewServer(api, serverConf, a.globalConfig.Spec.TracingSpec)
	server.StartNonBlocking()
}

func (a *ActionsRuntime) startGRPCServer(port int) error {
	api := grpc.NewAPI(a.runtimeConfig.ID, a.appChannel, a.directMessaging, a.actor, a)
	serverConf := grpc.NewServerConfig(a.runtimeConfig.ID, a.hostAddress, port)
	server := grpc.NewServer(api, serverConf, a.globalConfig.Spec.TracingSpec)
	err := server.StartNonBlocking()

	return err
}

func (a *ActionsRuntime) setHostAddress() error {
	a.hostAddress = os.Getenv(hostIPEnvVar)
	if a.hostAddress == "" {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return err
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					a.hostAddress = ipnet.IP.String()
					return nil
				}
			}
		}
	}

	return nil
}

func (a *ActionsRuntime) initInputBindings(registry bindings.BindingsRegistry) error {
	if a.appChannel == nil {
		return fmt.Errorf("app channel not initialized")
	}

	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "bindings") == 0 {
			req := channel.InvokeRequest{
				Method:   c.Metadata.Name,
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Options},
			}

			_, err := a.appChannel.InvokeMethod(&req)
			if err != nil {
				log.Error(err)
				continue
			}

			binding, err := registry.CreateInputBinding(c.Spec.Type)
			if err != nil {
				log.Warnf("failed to create input binding %s: %s", c.Spec.Type, err)
				continue
			}

			err = binding.Init(bindings.Metadata{
				ConnectionInfo: c.Spec.ConnectionInfo,
				Properties:     c.Spec.Properties,
				Name:           c.Metadata.Name,
			})
			if err != nil {
				log.Warnf("failed to init input binding %s: %s", c.Spec.Type, err)
				continue
			}

			a.inputBindings[c.Metadata.Name] = binding
		}
	}

	return nil
}

func (a *ActionsRuntime) initOutputBindings(registry bindings.BindingsRegistry) error {
	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "bindings") == 0 {
			binding, err := registry.CreateOutputBinding(c.Spec.Type)
			if err != nil {
				continue
			}

			if binding != nil {
				err := binding.Init(bindings.Metadata{
					ConnectionInfo: c.Spec.ConnectionInfo,
					Properties:     c.Spec.Properties,
				})
				if err != nil {
					log.Warnf("failed to init output binding %s: %s", c.Spec.Type, err)
					continue
				}

				a.outputBindings[c.Metadata.Name] = binding
			}
		}
	}

	return nil
}

func (a *ActionsRuntime) initState(registry state.StateStoreRegistry) error {
	state_loader.Load()

	for _, s := range a.components {
		if strings.Index(s.Spec.Type, "state") == 0 {
			store, err := registry.CreateStateStore(s.Spec.Type)
			if err != nil {
				log.Warnf("error creating state store %s: %s", s.Spec.Type, err)
				continue
			}

			if store != nil {
				err := store.Init(state.Metadata{
					ConnectionInfo: s.Spec.ConnectionInfo,
					Properties:     s.Spec.Properties,
				})
				if err != nil {
					log.Warnf("error initializing state store %s: %s", s.Spec.Type, err)
					continue
				}

				a.stateStore = store
				break
			}
		}
	}

	return nil
}

func (a *ActionsRuntime) getSubscribedTopicsFromApp() []string {
	topics := []string{}

	if a.appChannel == nil {
		return topics
	}

	req := &channel.InvokeRequest{
		Method:   "actions/subscribe",
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
	}

	resp, err := a.appChannel.InvokeMethod(req)
	if err == nil && http.GetStatusCodeFromMetadata(resp.Metadata) == 200 {
		err := json.Unmarshal(resp.Data, &topics)
		if err != nil {
			log.Errorf("error getting topics from app: %s", err)
		}
	}

	return topics
}

func (a *ActionsRuntime) initPubSub() error {
	for _, c := range a.components {
		if strings.Index(c.Spec.Type, "pubsub") == 0 {
			pubSub, err := a.pubSubRegistry.CreatePubSub(c.Spec.Type)
			if err != nil {
				log.Warnf("error creating pub sub %s: %s", c.Spec.Type, err)
				continue
			}

			properties := c.Spec.Properties
			if properties == nil {
				properties = make(map[string]string, 1)
			}
			properties["consumerID"] = a.runtimeConfig.ID

			err = pubSub.Init(pubsub.Metadata{
				ConnectionInfo: c.Spec.ConnectionInfo,
				Properties:     properties,
			})
			if err != nil {
				log.Warnf("error initializing pub sub %s: %s", c.Spec.Type, err)
				continue
			}

			a.pubSub = pubSub
			break
		}
	}

	if a.pubSub != nil && a.appChannel != nil {
		topics := a.getSubscribedTopicsFromApp()
		for _, t := range topics {
			err := a.pubSub.Subscribe(pubsub.SubscribeRequest{
				Topic: t,
			}, a.onNewPublishedMessage)
			if err != nil {
				log.Warnf("failed to subscribe to topic %s: %s", t, err)
			}
		}
	}

	return nil
}

func (a *ActionsRuntime) onNewPublishedMessage(msg *pubsub.NewMessage) error {
	req := channel.InvokeRequest{
		Method:   msg.Topic,
		Payload:  msg.Data,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Post},
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil || http.GetStatusCodeFromMetadata(resp.Metadata) != 200 {
		return fmt.Errorf("error from app: %s", err)
	}

	return nil
}

func (a *ActionsRuntime) initActors() error {
	actorConfig := actors.NewConfig(a.hostAddress, a.runtimeConfig.ID, a.runtimeConfig.PlacementServiceAddress, a.appConfig.Entities,
		a.runtimeConfig.GRPCPort, a.appConfig.ActorScanInterval, a.appConfig.ActorIdleTimeout)
	act := actors.NewActors(a.stateStore, a.appChannel, a.grpc.GetGRPCConnection, actorConfig)
	err := act.Init()
	a.actor = act
	return err
}

func (a *ActionsRuntime) loadComponents() error {
	var loader components.ComponentLoader

	switch a.runtimeConfig.Mode {
	case modes.KubernetesMode:
		loader = components.NewKubernetesComponents(a.runtimeConfig.Kubernetes)
	case modes.StandaloneMode:
		loader = components.NewStandaloneComponents(a.runtimeConfig.Standalone)
	default:
		return fmt.Errorf("components loader for mode %s not found", a.runtimeConfig.Mode)
	}

	allComponents, err := loader.LoadComponents()
	if err != nil {
		return err
	}

	a.components = allComponents

	for _, c := range a.components {
		log.Infof("loaded component %s (%s)", c.Metadata.Name, c.Spec.Type)
	}

	return nil
}

// Stop allows for a graceful shutdown of all runtime internal operations or components
func (a *ActionsRuntime) Stop() {
	log.Info("stop command issued. Shutting down all operations")
}

func (a *ActionsRuntime) blockUntilAppIsReady() {
	if a.runtimeConfig.ApplicationPort <= 0 {
		return
	}

	log.Infof("application protocol: %s. waiting on port %v", string(a.runtimeConfig.ApplicationProtocol), a.runtimeConfig.ApplicationPort)

	for {
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("localhost", fmt.Sprintf("%v", a.runtimeConfig.ApplicationPort)), time.Millisecond*500)
		if conn != nil {
			conn.Close()
			break
		}
	}

	log.Infof("application discovered on port %v", a.runtimeConfig.ApplicationPort)
}

func (a *ActionsRuntime) loadAppConfiguration() {
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

	return
}

func (a *ActionsRuntime) getConfigurationHTTP() (*config.ApplicationConfig, error) {
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

func (a *ActionsRuntime) getConfigurationGRPC() (*config.ApplicationConfig, error) {
	client := pb.NewAppClient(a.grpc.AppClient)
	resp, err := client.GetConfig(context.Background(), &empty.Empty{})
	if err != nil {
		return nil, err
	}

	if resp != nil {
		return &config.ApplicationConfig{
			Entities: resp.Entities,
		}, nil
	}

	return nil, nil
}

func (a *ActionsRuntime) createAppChannel() error {
	if a.runtimeConfig.ApplicationPort > 0 {
		var channelCreatorFn func(port int) (channel.AppChannel, error)

		switch a.runtimeConfig.ApplicationProtocol {
		case GRPCProtocol:
			channelCreatorFn = a.grpc.CreateLocalChannel
		case HTTPProtocol:
			channelCreatorFn = http_channel.CreateLocalChannel
		default:
			return fmt.Errorf("cannot create app channel for protocol %s", string(a.runtimeConfig.ApplicationProtocol))
		}

		ch, err := channelCreatorFn(a.runtimeConfig.ApplicationPort)
		if err != nil {
			return err
		}

		a.appChannel = ch
	}

	return nil
}
