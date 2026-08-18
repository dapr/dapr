/*
Copyright 2026 The Dapr Authors
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

package kubernetes

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// mockOperatorWFACL embeds UnimplementedOperatorServer so that
// ListWorkflowAccessPolicy returns codes.Unimplemented, matching the
// behaviour of an older (N-1) control plane that has not yet added the
// RPC. The loader must treat this as "no policies" rather than a fatal
// error, otherwise daprd cannot start under version skew.
type mockOperatorWFACL struct {
	operatorv1pb.UnimplementedOperatorServer
}

func TestLoadWorkflowAccessPoliciesUnimplemented(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, &mockOperatorWFACL{})
	errCh := make(chan error, 1)
	t.Cleanup(func() {
		s.Stop()
		require.NoError(t, <-errCh)
	})
	go func() { errCh <- s.Serve(lis) }()

	loader := &workflowAccessPolicies{
		client:    getOperatorClient(fmt.Sprintf("localhost:%d", port)),
		namespace: "default",
		appID:     "test-app",
	}

	policies, err := loader.Load(context.Background())
	require.NoError(t, err)
	assert.Nil(t, policies)
}
