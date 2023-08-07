/*
Copyright 2023 The Dapr Authors
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

	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type endpoint struct {
	operatorpb.Operator_HTTPEndpointUpdateClient
}

//nolint:unused
func (e *endpoint) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error) {
	resp, err := opclient.ListHTTPEndpoints(ctx, &operatorpb.ListHTTPEndpointsRequest{
		Namespace: ns,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetHttpEndpoints(), nil
}

//nolint:unused
func (e *endpoint) close() error {
	if e.Operator_HTTPEndpointUpdateClient != nil {
		return e.Operator_HTTPEndpointUpdateClient.CloseSend()
	}
	return nil
}

//nolint:unused
func (e *endpoint) recv() (*loader.Event[httpendapi.HTTPEndpoint], error) {
	event, err := e.Operator_HTTPEndpointUpdateClient.Recv()
	if err != nil {
		return nil, err
	}

	var endpoint httpendapi.HTTPEndpoint
	if err := json.Unmarshal(event.HttpEndpoints, &endpoint); err != nil {
		return nil, fmt.Errorf("failed to deserializing httpendpoint: %s", err)
	}

	return &loader.Event[httpendapi.HTTPEndpoint]{
		Resource: endpoint,
		Type:     event.Type,
	}, nil
}

//nolint:unused
func (e *endpoint) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) error {
	stream, err := opclient.HTTPEndpointUpdate(ctx, &operatorpb.HTTPEndpointUpdateRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return err
	}

	e.Operator_HTTPEndpointUpdateClient = stream
	return nil
}
