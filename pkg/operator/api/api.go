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

package api

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

const (
	APIVersionV1alpha1    = "dapr.io/v1alpha1"
	APIVersionV2alpha1    = "dapr.io/v2alpha1"
	kubernetesSecretStore = "kubernetes"
)

var log = logger.NewLogger("dapr.operator.api")

type Options struct {
	Client   client.Client
	Security security.Provider
	Port     int
}

// Server runs the Dapr API server for components and configurations.
type Server interface {
	Run(context.Context) error
	Ready(context.Context) error
	OnComponentUpdated(context.Context, operatorv1pb.ResourceEventType, *componentsapi.Component)
	OnHTTPEndpointUpdated(context.Context, *httpendpointsapi.HTTPEndpoint)
}

type ComponentUpdateEvent struct {
	Component *componentsapi.Component
	EventType operatorv1pb.ResourceEventType
}

type apiServer struct {
	operatorv1pb.UnimplementedOperatorServer
	Client client.Client
	sec    security.Provider
	port   string
	// notify all dapr runtime
	connLock               sync.Mutex
	endpointLock           sync.Mutex
	allConnUpdateChan      map[string]chan *ComponentUpdateEvent
	allEndpointsUpdateChan map[string]chan *httpendpointsapi.HTTPEndpoint
	readyCh                chan struct{}
	running                atomic.Bool
}

// NewAPIServer returns a new API server.
func NewAPIServer(opts Options) Server {
	return &apiServer{
		Client:                 opts.Client,
		sec:                    opts.Security,
		port:                   strconv.Itoa(opts.Port),
		allConnUpdateChan:      make(map[string]chan *ComponentUpdateEvent),
		allEndpointsUpdateChan: make(map[string]chan *httpendpointsapi.HTTPEndpoint),
		readyCh:                make(chan struct{}),
	}
}

// Run starts a new gRPC server.
func (a *apiServer) Run(ctx context.Context) error {
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("api server already running")
	}

	log.Infof("Starting gRPC server on port %s", a.port)

	sec, err := a.sec.Handler(ctx)
	if err != nil {
		return err
	}

	s := grpc.NewServer(sec.GRPCServerOptionMTLS())
	operatorv1pb.RegisterOperatorServer(s, a)

	lis, err := net.Listen("tcp", ":"+a.port)
	if err != nil {
		return fmt.Errorf("error starting tcp listener: %w", err)
	}
	close(a.readyCh)

	errCh := make(chan error)
	go func() {
		if rErr := s.Serve(lis); rErr != nil {
			errCh <- fmt.Errorf("gRPC server error: %w", rErr)
			return
		}
		errCh <- nil
	}()

	// Block until context is done
	<-ctx.Done()

	a.connLock.Lock()
	for key, ch := range a.allConnUpdateChan {
		close(ch)
		delete(a.allConnUpdateChan, key)
	}
	a.connLock.Unlock()

	s.GracefulStop()
	err = <-errCh
	if err != nil {
		return err
	}
	err = lis.Close()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("error closing listener: %w", err)
	}
	return nil
}

func (a *apiServer) OnComponentUpdated(ctx context.Context, eventType operatorv1pb.ResourceEventType, component *componentsapi.Component) {
	a.connLock.Lock()
	var wg sync.WaitGroup
	wg.Add(len(a.allConnUpdateChan))
	for _, connUpdateChan := range a.allConnUpdateChan {
		go func(connUpdateChan chan *ComponentUpdateEvent) {
			defer wg.Done()
			select {
			case connUpdateChan <- &ComponentUpdateEvent{
				Component: component,
				EventType: eventType,
			}:
			case <-ctx.Done():
			}
		}(connUpdateChan)
	}
	wg.Wait()
	a.connLock.Unlock()
}

func (a *apiServer) OnHTTPEndpointUpdated(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint) {
	a.endpointLock.Lock()
	for _, endpointUpdateChan := range a.allEndpointsUpdateChan {
		go func(endpointUpdateChan chan *httpendpointsapi.HTTPEndpoint) {
			endpointUpdateChan <- endpoint
		}(endpointUpdateChan)
	}
	a.endpointLock.Unlock()
}

func (a *apiServer) Ready(ctx context.Context) error {
	select {
	case <-a.readyCh:
		return nil
	case <-ctx.Done():
		return errors.New("timeout waiting for api server to be ready")
	}
}

// GetConfiguration returns a Dapr configuration.
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var config configurationapi.Configuration
	if err := a.Client.Get(ctx, key, &config); err != nil {
		return nil, fmt.Errorf("error getting configuration %s/%s: %w", in.GetNamespace(), in.GetName(), err)
	}
	b, err := json.Marshal(&config)
	if err != nil {
		return nil, fmt.Errorf("error marshalling configuration: %w", err)
	}
	return &operatorv1pb.GetConfigurationResponse{
		Configuration: b,
	}, nil
}

// ListComponents returns a list of Dapr components.
func (a *apiServer) ListComponents(ctx context.Context, in *operatorv1pb.ListComponentsRequest) (*operatorv1pb.ListComponentResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	var components componentsapi.ComponentList
	if err := a.Client.List(ctx, &components, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error getting components: %w", err)
	}
	resp := &operatorv1pb.ListComponentResponse{
		Components: [][]byte{},
	}
	for i := range components.Items {
		c := components.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		err := processComponentSecrets(ctx, &c, in.GetNamespace(), a.Client)
		if err != nil {
			log.Warnf("error processing component %s secrets from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			return &operatorv1pb.ListComponentResponse{}, err
		}

		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error marshalling component %s from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			continue
		}
		resp.Components = append(resp.GetComponents(), b)
	}
	return resp, nil
}

func processComponentSecrets(ctx context.Context, component *componentsapi.Component, namespace string, kubeClient client.Client) error {
	for i, m := range component.Spec.Metadata {
		if m.SecretKeyRef.Name != "" && (component.Auth.SecretStore == kubernetesSecretStore || component.Auth.SecretStore == "") {
			var secret corev1.Secret

			err := kubeClient.Get(ctx, types.NamespacedName{
				Name:      m.SecretKeyRef.Name,
				Namespace: namespace,
			}, &secret)
			if err != nil {
				return err
			}

			key := m.SecretKeyRef.Key
			if key == "" {
				key = m.SecretKeyRef.Name
			}

			val, ok := secret.Data[key]
			if ok {
				enc := b64.StdEncoding.EncodeToString(val)
				jsonEnc, err := json.Marshal(enc)
				if err != nil {
					return err
				}
				component.Spec.Metadata[i].Value = commonapi.DynamicValue{
					JSON: v1.JSON{
						Raw: jsonEnc,
					},
				}
			}
		}
	}

	return nil
}

func pairNeedsSecretExtraction(ref commonapi.SecretKeyRef, auth httpendpointsapi.Auth) bool {
	return ref.Name != "" && (auth.SecretStore == kubernetesSecretStore || auth.SecretStore == "")
}

func getSecret(ctx context.Context, name, namespace string, ref commonapi.SecretKeyRef, kubeClient client.Client) (commonapi.DynamicValue, error) {
	var secret corev1.Secret

	err := kubeClient.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return commonapi.DynamicValue{}, err
	}

	key := ref.Key
	if key == "" {
		key = ref.Name
	}

	val, ok := secret.Data[key]
	if ok {
		enc := b64.StdEncoding.EncodeToString(val)
		jsonEnc, err := json.Marshal(enc)
		if err != nil {
			return commonapi.DynamicValue{}, err
		}

		return commonapi.DynamicValue{
			JSON: v1.JSON{
				Raw: jsonEnc,
			},
		}, nil
	}

	return commonapi.DynamicValue{}, nil
}

func processHTTPEndpointSecrets(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint, namespace string, kubeClient client.Client) error {
	for i, header := range endpoint.Spec.Headers {
		if pairNeedsSecretExtraction(header.SecretKeyRef, endpoint.Auth) {
			v, err := getSecret(ctx, header.SecretKeyRef.Name, namespace, header.SecretKeyRef, kubeClient)
			if err != nil {
				return err
			}

			endpoint.Spec.Headers[i].Value = v
		}
	}

	if endpoint.HasTLSClientCertSecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.Certificate.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.Certificate.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.Certificate.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.Certificate.Value = &v
	}

	if endpoint.HasTLSPrivateKeySecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.PrivateKey.Value = &v
	}

	if endpoint.HasTLSRootCASecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.RootCA.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.RootCA.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.RootCA.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.RootCA.Value = &v
	}

	return nil
}

// ListSubscriptions returns a list of Dapr pub/sub subscriptions.
func (a *apiServer) ListSubscriptions(ctx context.Context, in *emptypb.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	return a.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{})
}

// ListSubscriptionsV2 returns a list of Dapr pub/sub subscriptions. Use ListSubscriptionsRequest to expose pod info.
func (a *apiServer) ListSubscriptionsV2(ctx context.Context, in *operatorv1pb.ListSubscriptionsRequest) (*operatorv1pb.ListSubscriptionsResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{},
	}

	// Only the latest/storage version needs to be returned.
	var subsV2alpha1 subscriptionsapiV2alpha1.SubscriptionList
	if err := a.Client.List(ctx, &subsV2alpha1, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error getting subscriptions: %w", err)
	}
	for i := range subsV2alpha1.Items {
		s := subsV2alpha1.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		if s.APIVersion != APIVersionV2alpha1 {
			continue
		}
		b, err := json.Marshal(&s)
		if err != nil {
			log.Warnf("error marshalling subscription for pod %s/%s: %s", in.GetNamespace(), in.GetPodName(), err)
			continue
		}
		resp.Subscriptions = append(resp.GetSubscriptions(), b)
	}

	return resp, nil
}

// GetResiliency returns a specified resiliency object.
func (a *apiServer) GetResiliency(ctx context.Context, in *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetResiliencyResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var resiliencyConfig resiliencyapi.Resiliency
	if err := a.Client.Get(ctx, key, &resiliencyConfig); err != nil {
		return nil, fmt.Errorf("error getting resiliency: %w", err)
	}
	b, err := json.Marshal(&resiliencyConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshalling resiliency: %w", err)
	}
	return &operatorv1pb.GetResiliencyResponse{
		Resiliency: b,
	}, nil
}

// ListResiliency gets the list of applied resiliencies.
func (a *apiServer) ListResiliency(ctx context.Context, in *operatorv1pb.ListResiliencyRequest) (*operatorv1pb.ListResiliencyResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListResiliencyResponse{
		Resiliencies: [][]byte{},
	}

	var resiliencies resiliencyapi.ResiliencyList
	if err := a.Client.List(ctx, &resiliencies, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing resiliencies: %w", err)
	}

	for _, item := range resiliencies.Items {
		b, err := json.Marshal(item)
		if err != nil {
			log.Warnf("Error unmarshalling resiliency: %s", err)
			continue
		}
		resp.Resiliencies = append(resp.GetResiliencies(), b)
	}

	return resp, nil
}

// ComponentUpdate updates Dapr sidecars whenever a component in the cluster is modified.
// TODO: @joshvanl: Authorize pod name and namespace matches the SPIFFE ID of
// the caller.
func (a *apiServer) ComponentUpdate(in *operatorv1pb.ComponentUpdateRequest, srv operatorv1pb.Operator_ComponentUpdateServer) error { //nolint:nosnakecase
	if err := a.authzRequest(srv.Context(), in.GetNamespace()); err != nil {
		return err
	}

	log.Info("sidecar connected for component updates")
	keyObj, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	key := keyObj.String()

	a.connLock.Lock()
	a.allConnUpdateChan[key] = make(chan *ComponentUpdateEvent)
	updateChan := a.allConnUpdateChan[key]
	a.connLock.Unlock()

	defer func() {
		a.connLock.Lock()
		defer a.connLock.Unlock()
		delete(a.allConnUpdateChan, key)
	}()

	updateComponentFunc := func(ctx context.Context, t operatorv1pb.ResourceEventType, c *componentsapi.Component) {
		if c.Namespace != in.GetNamespace() {
			return
		}

		err := processComponentSecrets(ctx, c, in.GetNamespace(), a.Client)
		if err != nil {
			log.Warnf("error processing component %s secrets from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error serializing component %s (%s) from pod %s/%s: %s", c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		err = srv.Send(&operatorv1pb.ComponentUpdateEvent{
			Component: b,
			Type:      t,
		})
		if err != nil {
			log.Warnf("error updating sidecar with component %s (%s) from pod %s/%s: %s", c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		log.Debugf("updated sidecar with component %s %s (%s) from pod %s/%s", t.String(), c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName())
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-srv.Context().Done():
			return nil
		case c, ok := <-updateChan:
			if !ok {
				return nil
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				updateComponentFunc(srv.Context(), c.EventType, c.Component)
			}()
		}
	}
}

// GetHTTPEndpoint returns a specified http endpoint object.
func (a *apiServer) GetHTTPEndpoint(ctx context.Context, in *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetHTTPEndpointResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var endpointConfig httpendpointsapi.HTTPEndpoint
	if err := a.Client.Get(ctx, key, &endpointConfig); err != nil {
		return nil, fmt.Errorf("error getting http endpoint: %w", err)
	}
	b, err := json.Marshal(&endpointConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshalling http endpoint: %w", err)
	}
	return &operatorv1pb.GetHTTPEndpointResponse{
		HttpEndpoint: b,
	}, nil
}

// ListHTTPEndpoints gets the list of applied http endpoints.
func (a *apiServer) ListHTTPEndpoints(ctx context.Context, in *operatorv1pb.ListHTTPEndpointsRequest) (*operatorv1pb.ListHTTPEndpointsResponse, error) {
	if err := a.authzRequest(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListHTTPEndpointsResponse{
		HttpEndpoints: [][]byte{},
	}

	var endpoints httpendpointsapi.HTTPEndpointList
	if err := a.Client.List(ctx, &endpoints, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing http endpoints: %w", err)
	}

	for i, item := range endpoints.Items {
		e := endpoints.Items[i]
		err := processHTTPEndpointSecrets(ctx, &e, item.Namespace, a.Client)
		if err != nil {
			log.Warnf("error processing secrets for http endpoint '%s/%s': %s", item.Namespace, item.Name, err)
			return &operatorv1pb.ListHTTPEndpointsResponse{}, err
		}

		b, err := json.Marshal(e)
		if err != nil {
			log.Warnf("Error unmarshalling http endpoints: %s", err)
			continue
		}
		resp.HttpEndpoints = append(resp.GetHttpEndpoints(), b)
	}

	return resp, nil
}

// HTTPEndpointUpdate updates Dapr sidecars whenever an http endpoint in the cluster is modified.
func (a *apiServer) HTTPEndpointUpdate(in *operatorv1pb.HTTPEndpointUpdateRequest, srv operatorv1pb.Operator_HTTPEndpointUpdateServer) error { //nolint:nosnakecase
	if err := a.authzRequest(srv.Context(), in.GetNamespace()); err != nil {
		return err
	}

	log.Info("sidecar connected for http endpoint updates")
	keyObj, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	key := keyObj.String()

	a.endpointLock.Lock()
	a.allEndpointsUpdateChan[key] = make(chan *httpendpointsapi.HTTPEndpoint, 1)
	updateChan := a.allEndpointsUpdateChan[key]
	a.endpointLock.Unlock()

	defer func() {
		a.endpointLock.Lock()
		defer a.endpointLock.Unlock()
		delete(a.allEndpointsUpdateChan, key)
	}()

	updateHTTPEndpointFunc := func(ctx context.Context, e *httpendpointsapi.HTTPEndpoint) {
		if e.Namespace != in.GetNamespace() {
			return
		}

		err := processHTTPEndpointSecrets(ctx, e, in.GetNamespace(), a.Client)
		if err != nil {
			log.Warnf("error processing http endpoint %s secrets from pod %s/%s: %s", e.Name, in.GetNamespace(), in.GetPodName(), err)
			return
		}
		b, err := json.Marshal(&e)
		if err != nil {
			log.Warnf("error serializing  http endpoint %s from pod %s/%s: %s", e.GetName(), in.GetNamespace(), in.GetPodName(), err)
			return
		}

		err = srv.Send(&operatorv1pb.HTTPEndpointUpdateEvent{
			HttpEndpoints: b,
		})
		if err != nil {
			log.Warnf("error updating sidecar with http endpoint %s from pod %s/%s: %s", e.GetName(), in.GetNamespace(), in.GetPodName(), err)
			return
		}

		log.Infof("updated sidecar with http endpoint %s from pod %s/%s", e.GetName(), in.GetNamespace(), in.GetPodName())
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-srv.Context().Done():
			return nil
		case c, ok := <-updateChan:
			if !ok {
				return nil
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				updateHTTPEndpointFunc(srv.Context(), c)
			}()
		}
	}
}
