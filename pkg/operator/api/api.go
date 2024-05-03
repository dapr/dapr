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
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api/informer"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
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
	Cache    cache.Cache
	Security security.Provider
	Port     int
}

// Server runs the Dapr API server for components and configurations.
type Server interface {
	Run(context.Context) error
	Ready(context.Context) error

	OnSubscriptionUpdated(context.Context, operatorv1pb.ResourceEventType, *subapi.Subscription)
	OnHTTPEndpointUpdated(context.Context, *httpendpointsapi.HTTPEndpoint)
}

type apiServer struct {
	operatorv1pb.UnimplementedOperatorServer
	Client client.Client
	sec    security.Provider
	port   string

	compInformer informer.Interface[componentsapi.Component]

	endpointLock              sync.Mutex
	allEndpointsUpdateChan    map[string]chan *httpendpointsapi.HTTPEndpoint
	allSubscriptionUpdateChan map[string]chan *SubscriptionUpdateEvent
	connLock                  sync.Mutex
	readyCh                   chan struct{}
	running                   atomic.Bool
}

// NewAPIServer returns a new API server.
func NewAPIServer(opts Options) Server {
	return &apiServer{
		Client: opts.Client,
		compInformer: informer.New[componentsapi.Component](informer.Options{
			Cache: opts.Cache,
		}),
		sec:                       opts.Security,
		port:                      strconv.Itoa(opts.Port),
		allEndpointsUpdateChan:    make(map[string]chan *httpendpointsapi.HTTPEndpoint),
		allSubscriptionUpdateChan: make(map[string]chan *SubscriptionUpdateEvent),
		readyCh:                   make(chan struct{}),
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

	return concurrency.NewRunnerManager(
		a.compInformer.Run,
		func(ctx context.Context) error {
			if err := s.Serve(lis); err != nil {
				return fmt.Errorf("gRPC server error: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			// Block until context is done
			<-ctx.Done()
			a.connLock.Lock()
			for key, ch := range a.allSubscriptionUpdateChan {
				close(ch)
				delete(a.allSubscriptionUpdateChan, key)
			}
			a.connLock.Unlock()
			s.GracefulStop()
			return nil
		},
	).Run(ctx)
}

func (a *apiServer) Ready(ctx context.Context) error {
	select {
	case <-a.readyCh:
		return nil
	case <-ctx.Done():
		return errors.New("timeout waiting for api server to be ready")
	}
}
