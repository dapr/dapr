/*
Copyright 2022 The Dapr Authors
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

package components

import (
	"context"
	"net"

	"github.com/dapr/kit/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// PluggableComponentTestServerFor is used for creating a client factory that bootstrap a service and a client for the given pluggable component.
func PluggableComponentTestServerFor[TServer any, TClient any](logger logger.Logger, registersvc func(*grpc.Server, TServer), clientFactory func(grpc.ClientConnInterface) TClient) func(svc TServer) (client TClient, cleanup func(), err error) {
	return func(srv TServer) (client TClient, cleanup func(), err error) {
		lis := bufconn.Listen(bufSize)
		s := grpc.NewServer()
		registersvc(s, srv)
		go func() {
			if serveErr := s.Serve(lis); serveErr != nil {
				logger.Debugf("Server exited with error: %v", serveErr)
			}
		}()
		ctx := context.Background()
		conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		}), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			var zero TClient
			return zero, nil, err
		}

		pcClient := clientFactory(conn)
		return pcClient, func() {
			lis.Close()
			conn.Close()
		}, nil
	}
}
