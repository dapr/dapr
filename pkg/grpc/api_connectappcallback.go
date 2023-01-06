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

package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) SetCreateAppCallbackListener(fn func() (int, error)) {
	a.createAppCallbackListener = fn
}

func (a *api) ConnectAppCallback(context.Context, *runtimev1pb.ConnectAppCallbackRequest) (*runtimev1pb.ConnectAppCallbackResponse, error) {
	// Timeout for accepting connections from clients before the ephemeral listener is terminated
	const connectionTimeout = 10 * time.Second

	// If createAppCallbackListener is nil, it means that the callback channel is not enabled
	var err error
	if a.createAppCallbackListener == nil {
		err = status.Errorf(codes.PermissionDenied, "callback channel is not enabled")
		apiServerLogger.Debug(err)
		return nil, err
	}

	// Start the listener, then return the port it's listening on to the caller
	port, err := a.createAppCallbackListener()
	if err != nil {
		err = status.Errorf(codes.Internal, err.Error())
		apiServerLogger.Debug(err)
		return nil, err
	}

	// In the meanwhile, return the response with the port
	return &runtimev1pb.ConnectAppCallbackResponse{
		Port: int32(port),
	}, nil
}
