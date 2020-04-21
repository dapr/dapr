// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/modes"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	invokeRemoteRetryCount = 3
)

// DirectMessaging is the API interface for invoking a remote app
type DirectMessaging interface {
	Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error)
}

type directMessaging struct {
	appChannel          channel.AppChannel
	connectionCreatorFn func(address, id string, skipTLS, recreateIfExists bool) (*grpc.ClientConn, error)
	appID               string
	mode                modes.DaprMode
	grpcPort            int
	namespace           string
	resolver            servicediscovery.Resolver
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(appID, namespace string, port int, mode modes.DaprMode, appChannel channel.AppChannel, grpcConnectionFn func(address, id string, skipTLS, recreateIfExists bool) (*grpc.ClientConn, error), resolver servicediscovery.Resolver) DirectMessaging {
	return &directMessaging{
		appChannel:          appChannel,
		connectionCreatorFn: grpcConnectionFn,
		appID:               appID,
		mode:                mode,
		grpcPort:            port,
		namespace:           namespace,
		resolver:            resolver,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	if req.Target == d.appID {
		return d.invokeLocal(req)
	}
	return d.invokeWithRetry(invokeRemoteRetryCount, d.invokeRemote, req)
}

// invokeWithRetry will call a remote endpoint for the specified number of retries and will only retry in the case of transient failures
// TODO: check why https://github.com/grpc-ecosystem/go-grpc-middleware/blob/master/retry/examples_test.go doesn't recover the connection when target
// Server shuts down.
func (d *directMessaging) invokeWithRetry(numRetries int, fn func(req *DirectMessageRequest) (*DirectMessageResponse, error), req *DirectMessageRequest) (*DirectMessageResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(req)
		if err == nil {
			return resp, nil
		}

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			address, addErr := d.getAddressFromMessageRequest(req)
			if addErr != nil {
				return nil, addErr
			}
			_, connErr := d.connectionCreatorFn(address, req.Target, false, true)
			if connErr != nil {
				return nil, connErr
			}
			continue
		}
		return resp, err
	}
	return nil, fmt.Errorf("failed to invoke target %s after %v retries", req.Target, numRetries)
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

func (d *directMessaging) getAddressFromMessageRequest(req *DirectMessageRequest) (string, error) {
	request := servicediscovery.ResolveRequest{ID: req.Target, Namespace: d.namespace, Port: d.grpcPort}
	address, err := d.resolver.ResolveID(request)
	if err != nil {
		return "", err
	}
	return address, nil
}

func (d *directMessaging) invokeRemote(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	address, err := d.getAddressFromMessageRequest(req)
	if err != nil {
		return nil, err
	}

	conn, err := d.connectionCreatorFn(address, req.Target, false, false)
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
