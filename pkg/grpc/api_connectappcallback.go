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
	"net"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) SetOnAppCallbackConnection(fn func(conn net.Conn)) {
	a.onAppCallbackConnection = fn
}

func (a *api) ConnectAppCallback(context.Context, *runtimev1pb.ConnectAppCallbackRequest) (*runtimev1pb.ConnectAppCallbackResponse, error) {
	// Timeout for accepting connections from clients before the ephemeral listener is terminated
	const connectionTimeout = 10 * time.Second

	// If onAppCallbackConnection is nil, it means that the callback channel is not enabled
	var err error
	if a.onAppCallbackConnection == nil {
		err = status.Errorf(codes.PermissionDenied, "callback channel is not enabled")
		apiServerLogger.Debug(err)
		return nil, err
	}

	// Create a new TCP listener that will accept connections from the client
	// This is listening on port "0" which means that the kernel will assign a random available port
	// TODO @ItalyPaleAle: use APIListenAddresses from the config
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to create address: %v", err)
		apiServerLogger.Debug(err)
		return nil, err
	}
	lis, err := net.ListenTCP("tcp", addr)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to create listener: %v", err)
		apiServerLogger.Debug(err)
		return nil, err
	}
	port := lis.Addr().(*net.TCPAddr).Port

	apiServerLogger.Debugf("Created ephemeral listener on port %d", port)

	// In a background goroutine, wait for the first client to establish a connection to the listener we just created
	// The first connectiopn to be established wins
	// There's also a timeout after which we will close the listener if no one connected to it
	connCh := make(chan any)
	go func() {
		conn, connErr := lis.Accept()
		if connErr != nil {
			connCh <- connErr
		} else {
			connCh <- conn
		}
		close(connCh)
	}()
	go func() {
		select {
		case <-time.After(connectionTimeout):
			// Timed out
			// Log, then exit the select block
			apiServerLogger.Warnf("Client did not connect to the ephemeral listener within %v", connectionTimeout)
		case msg := <-connCh:
			if msg == nil {
				// Exit the select block
				break
			}
			switch v := msg.(type) {
			case error:
				// Log, then exit the select block
				apiServerLogger.Errorf("Error while trying to accept connection to the ephemeral listener: %v", v)
			case net.Conn:
				apiServerLogger.Infof("Established client connection on the ephemeral listener from %v", v.RemoteAddr())
				a.onAppCallbackConnection(v)
			}
		}

		// Close the listener - whether we have a connection or not, we don't need it anymore
		innerErr := lis.Close()
		if innerErr != nil {
			apiServerLogger.Errorf("Failed to close epehemeral listener: %v", innerErr)
		}
	}()

	// In the meanwhile, return the response with the port
	return &runtimev1pb.ConnectAppCallbackResponse{
		Port: int32(port),
	}, nil
}
