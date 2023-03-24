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

package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestConnectToServer(t *testing.T) {
	ctx := context.Background()
	t.Run("when grpc get opts return an error connectToServer should return an error", func(t *testing.T) {
		_, err := newPlacementClient(ctx, "", func() ([]grpc.DialOption, error) {
			return nil, errEstablishingTLSConn
		})
		assert.Equal(t, err, errEstablishingTLSConn)
	})
	t.Run("when grpc dial returns an error connectToServer should return an error", func(t *testing.T) {
		_, err := newPlacementClient(ctx, "", func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})
		assert.Error(t, err)
	})
	t.Run("when new placement stream returns an error connectToServer should return an error", func(t *testing.T) {
		conn, cleanup := newTestServerWithOpts(t) // do not register the placement stream server
		defer cleanup()
		_, err := newPlacementClient(ctx, conn, func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})
		assert.Error(t, err)
	})
	t.Run("when connectToServer succeeds it should return no error", func(t *testing.T) {
		conn, _, cleanup := newTestServer(t) // do not register the placement stream server
		defer cleanup()

		_, err := newPlacementClient(ctx, conn, getGrpcOptsGetter([]string{conn}, nil))
		assert.NoError(t, err)
	})
}
