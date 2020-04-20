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

	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/logger"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const serverPort = 6500

var log = logger.NewLogger("dapr.operator.api")

//Server runs the Dapr API server for components and configurations
type Server interface {
	Run(certChain *dapr_credentials.CertChain)
	OnComponentUpdated(component *v1alpha1.Component)
}

type apiServer struct {
	Client     scheme.Interface
	updateChan chan (*v1alpha1.Component)
}

// NewAPIServer returns a new API server
func NewAPIServer(client scheme.Interface) Server {
	return &apiServer{
		Client:     client,
		updateChan: make(chan *v1alpha1.Component, 1),
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

func (a *apiServer) OnComponentUpdated(component *v1alpha1.Component) {
	a.updateChan <- component
}

// GetConfiguration returns a Dapr configuration
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	config, err := a.Client.ConfigurationV1alpha1().Configurations(in.Namespace).Get(in.Name, meta_v1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting configuration: %s", err)
	}
	b, err := json.Marshal(&config)
	if err != nil {
		return nil, fmt.Errorf("error marshalling configuration: %s", err)
	}
	return &operatorv1pb.GetConfigurationResponse{
		Configuration: &any.Any{
			Value: b,
		},
	}, nil
}

// GetComponents returns a list of Dapr components
func (a *apiServer) GetComponents(ctx context.Context, in *empty.Empty) (*operatorv1pb.GetComponentResponse, error) {
	components, err := a.Client.ComponentsV1alpha1().Components(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting components: %s", err)
	}
	resp := &operatorv1pb.GetComponentResponse{
		Components: []*any.Any{},
	}
	for _, c := range components.Items {
		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error marshalling component: %s", err)
			continue
		}
		resp.Components = append(resp.Components, &any.Any{
			Value: b,
		})
	}
	return resp, nil
}

// ComponentUpdate updates Dapr sidecars whenever a component in the cluster is modified
func (a *apiServer) ComponentUpdate(in *empty.Empty, srv operatorv1pb.Operator_ComponentUpdateServer) error {
	log.Info("sidecar connected for component updates")

	for c := range a.updateChan {
		go func(c *v1alpha1.Component) {
			b, err := json.Marshal(&c)
			if err != nil {
				log.Warnf("error serializing component %s (%s): %s", c.GetName(), c.Spec.Type, err)
				return
			}
			err = srv.Send(&operatorv1pb.ComponentUpdateEvent{
				Component: &any.Any{
					Value: b,
				},
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
