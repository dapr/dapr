/*
Copyright 2024 The Dapr Authors
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

package operator

import (
	"context"
	"encoding/json"
	"fmt"

	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type httpEndpoints struct {
	operatorpb.Operator_HTTPEndpointUpdateClient
}

// The go linter does not yet understand that these functions are being used by
// the generic operator.
//
//nolint:unused
func (h *httpEndpoints) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error) {
	resp, err := opclient.ListHTTPEndpoints(ctx, &operatorpb.ListHTTPEndpointsRequest{
		Namespace: ns,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetHttpEndpoints(), nil
}

//nolint:unused
func (h *httpEndpoints) close() error {
	if h.Operator_HTTPEndpointUpdateClient != nil {
		return h.Operator_HTTPEndpointUpdateClient.CloseSend()
	}
	return nil
}

//nolint:unused
func (h *httpEndpoints) recv(context.Context) (*loader.Event[httpendpointapi.HTTPEndpoint], error) {
	event, err := h.Operator_HTTPEndpointUpdateClient.Recv()
	if err != nil {
		return nil, err
	}

	var endpoint httpendpointapi.HTTPEndpoint
	if err := json.Unmarshal(event.GetHttpEndpoints(), &endpoint); err != nil {
		return nil, fmt.Errorf("failed to deserialize http endpoint: %w", err)
	}

	return &loader.Event[httpendpointapi.HTTPEndpoint]{
		Resource: endpoint,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (h *httpEndpoints) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) error {
	stream, err := opclient.HTTPEndpointUpdate(ctx, &operatorpb.HTTPEndpointUpdateRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return err
	}

	h.Operator_HTTPEndpointUpdateClient = stream
	return nil
}
