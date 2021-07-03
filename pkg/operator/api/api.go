// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/kit/logger"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

const serverPort = 6500

var log = logger.NewLogger("dapr.operator.api")

// Server runs the Dapr API server for components and configurations.
type Server interface {
	Run(certChain *dapr_credentials.CertChain)
	OnComponentUpdated(component *componentsapi.Component)
}

type apiServer struct {
	Client client.Client
	// notify all dapr runtime
	connLock          sync.Mutex
	allConnUpdateChan map[string]chan *componentsapi.Component
}

// NewAPIServer returns a new API server.
func NewAPIServer(client client.Client) Server {
	return &apiServer{
		Client:            client,
		allConnUpdateChan: make(map[string]chan *componentsapi.Component),
	}
}

// Run starts a new gRPC server.
func (a *apiServer) Run(certChain *dapr_credentials.CertChain) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%v", serverPort))
	if err != nil {
		log.Fatal("error starting tcp listener: %s", err)
	}

	opts, err := dapr_credentials.GetServerOptions(certChain)
	if err != nil {
		log.Fatal("error creating gRPC options: %s", err)
	}
	s := grpc.NewServer(opts...)
	operatorv1pb.RegisterOperatorServer(s, a)

	log.Info("starting gRPC server")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}

func (a *apiServer) OnComponentUpdated(component *componentsapi.Component) {
	a.connLock.Lock()
	for _, connUpdateChan := range a.allConnUpdateChan {
		connUpdateChan <- component
	}
	a.connLock.Unlock()
}

// GetConfiguration returns a Dapr configuration.
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	key := types.NamespacedName{Namespace: in.Namespace, Name: in.Name}
	var config configurationapi.Configuration
	if err := a.Client.Get(ctx, key, &config); err != nil {
		return nil, errors.Wrap(err, "error getting configuration")
	}
	b, err := json.Marshal(&config)
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling configuration")
	}
	return &operatorv1pb.GetConfigurationResponse{
		Configuration: b,
	}, nil
}

// ListComponents returns a list of Dapr components.
func (a *apiServer) ListComponents(ctx context.Context, in *emptypb.Empty) (*operatorv1pb.ListComponentResponse, error) {
	var components componentsapi.ComponentList
	if err := a.Client.List(ctx, &components); err != nil {
		return nil, errors.Wrap(err, "error getting components")
	}
	resp := &operatorv1pb.ListComponentResponse{
		Components: [][]byte{},
	}
	for i := range components.Items {
		c := components.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error marshalling component: %s", err)
			continue
		}
		resp.Components = append(resp.Components, b)
	}
	return resp, nil
}

// ListSubscriptions returns a list of Dapr pub/sub subscriptions.
func (a *apiServer) ListSubscriptions(ctx context.Context, in *emptypb.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	var subs subscriptionsapi.SubscriptionList
	if err := a.Client.List(ctx, &subs); err != nil {
		return nil, errors.Wrap(err, "error getting subscriptions")
	}
	resp := &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{},
	}
	for i := range subs.Items {
		s := subs.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		b, err := json.Marshal(&s)
		if err != nil {
			log.Warnf("error marshalling subscription: %s", err)
			continue
		}
		resp.Subscriptions = append(resp.Subscriptions, b)
	}
	return resp, nil
}

// ComponentUpdate updates Dapr sidecars whenever a component in the cluster is modified.
func (a *apiServer) ComponentUpdate(in *emptypb.Empty, srv operatorv1pb.Operator_ComponentUpdateServer) error {
	log.Info("sidecar connected for component updates")
	key := uuid.New().String()
	a.connLock.Lock()
	a.allConnUpdateChan[key] = make(chan *componentsapi.Component, 1)
	updateChan := a.allConnUpdateChan[key]
	a.connLock.Unlock()
	defer func() {
		close(updateChan)
		a.connLock.Lock()
		delete(a.allConnUpdateChan, key)
		a.connLock.Unlock()
	}()

	for c := range updateChan {
		go func(c *componentsapi.Component) {
			b, err := json.Marshal(&c)
			if err != nil {
				log.Warnf("error serializing component %s (%s): %s", c.GetName(), c.Spec.Type, err)
				return
			}
			err = srv.Send(&operatorv1pb.ComponentUpdateEvent{
				Component: b,
			})
			if err != nil {
				log.Warnf("error updating sidecar with component %s (%s): %s", c.GetName(), c.Spec.Type, err)
				return
			}
			log.Infof("updated sidecar with component %s (%s)", c.GetName(), c.Spec.Type)
		}(c)
	}
	return nil
}
