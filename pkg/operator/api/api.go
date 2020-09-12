// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/logger"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const serverPort = 6500

var log = logger.NewLogger("dapr.operator.api")

//Server runs the Dapr API server for components and configurations
type Server interface {
	Run(certChain *dapr_credentials.CertChain)
	OnComponentUpdated(component *componentsapi.Component)
}

type apiServer struct {
	Client     client.Client
	updateChan chan (*componentsapi.Component)
}

// NewAPIServer returns a new API server
func NewAPIServer(client client.Client) Server {
	return &apiServer{
		Client:     client,
		updateChan: make(chan *componentsapi.Component, 1),
	}
}

// Run starts a new gRPC server
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
	// TODO: Process updates from components
}

// GetConfiguration returns a Dapr configuration
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

// GetComponents returns a list of Dapr components
func (a *apiServer) ListComponents(ctx context.Context, in *empty.Empty) (*operatorv1pb.ListComponentResponse, error) {
	var components componentsapi.ComponentList
	if err := a.Client.List(ctx, &components); err != nil {
		return nil, errors.Wrap(err, "error getting components")
	}
	resp := &operatorv1pb.ListComponentResponse{
		Components: [][]byte{},
	}
	for _, c := range components.Items {
		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error marshalling component: %s", err)
			continue
		}
		resp.Components = append(resp.Components, b)
	}
	return resp, nil
}

// ListSubscriptions returns a list of Dapr pub/sub subscriptions
func (a *apiServer) ListSubscriptions(ctx context.Context, in *empty.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	var subs subscriptionsapi.SubscriptionList
	if err := a.Client.List(ctx, &subs); err != nil {
		return nil, errors.Wrap(err, "error getting subscriptions")
	}
	resp := &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{},
	}
	for _, s := range subs.Items {
		b, err := json.Marshal(&s)
		if err != nil {
			log.Warnf("error marshalling subscription: %s", err)
			continue
		}
		resp.Subscriptions = append(resp.Subscriptions, b)
	}
	return resp, nil
}

// ComponentUpdate updates Dapr sidecars whenever a component in the cluster is modified
func (a *apiServer) ComponentUpdate(in *empty.Empty, srv operatorv1pb.Operator_ComponentUpdateServer) error {
	log.Info("sidecar connected for component updates")

	for c := range a.updateChan {
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
