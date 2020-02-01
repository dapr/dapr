// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"errors"
	"time"

	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/modes"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
)

// DirectMessaging is the API interface for invoking a remote app
type DirectMessaging interface {
	Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error)
}

type directMessaging struct {
	appChannel          channel.AppChannel
	connectionCreatorFn func(address, id string, skipTLS bool) (*grpc.ClientConn, error)
	daprID              string
	mode                modes.DaprMode
	grpcPort            int
	namespace           string
	resolver            servicediscovery.Resolver
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(daprID, namespace string, port int, mode modes.DaprMode, appChannel channel.AppChannel, grpcConnectionFn func(address, id string, skipTLS bool) (*grpc.ClientConn, error), resolver servicediscovery.Resolver) DirectMessaging {
	return &directMessaging{
		appChannel:          appChannel,
		connectionCreatorFn: grpcConnectionFn,
		daprID:              daprID,
		mode:                mode,
		grpcPort:            port,
		namespace:           namespace,
		resolver:            resolver,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	var invokeFn func(*DirectMessageRequest) (*DirectMessageResponse, error)

	if req.Target == d.daprID {
		invokeFn = d.invokeLocal
	} else {
		invokeFn = d.invokeRemote
	}

	return invokeFn(req)
}

func (d *directMessaging) invokeLocal(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	localInvokeReq := channel.InvokeRequest{
		Metadata: req.Metadata,
		Method:   req.Method,
		Payload:  req.Data,
	}

	resp, err := d.appChannel.InvokeMethod(&localInvokeReq)
	if err != nil {
		return nil, err
	}

	return &DirectMessageResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func (d *directMessaging) invokeRemote(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	request := servicediscovery.ResolveRequest{ID: req.Target, Namespace: d.namespace, Port: d.grpcPort}
	address, err := d.resolver.ResolveID(request)
	if err != nil {
		return nil, err
	}

	conn, err := d.connectionCreatorFn(address, req.Target, false)
	if err != nil {
		return nil, err
	}

	msg := daprinternal_pb.LocalCallEnvelope{
		Data:     &any.Any{Value: req.Data},
		Metadata: req.Metadata,
		Method:   req.Method,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	client := daprinternal_pb.NewDaprInternalClient(conn)
	resp, err := client.CallLocal(ctx, &msg)
	if err != nil {
		return nil, err
	}

	return &DirectMessageResponse{
		Data:     resp.Data.Value,
		Metadata: resp.Metadata,
	}, nil
}
