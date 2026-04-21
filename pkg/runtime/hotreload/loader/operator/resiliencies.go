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

	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type resiliencies struct {
	operatorpb.Operator_ResiliencyUpdateClient
}

// The go linter does not yet understand that these functions are being used by
// the generic operator.
//
//nolint:unused
func (r *resiliencies) list(ctx context.Context, opclient operatorpb.OperatorClient, ns string) ([][]byte, error) {
	resp, err := opclient.ListResiliency(ctx, &operatorpb.ListResiliencyRequest{
		Namespace: ns,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetResiliencies(), nil
}

//nolint:unused
func (r *resiliencies) close() error {
	if r.Operator_ResiliencyUpdateClient != nil {
		return r.CloseSend()
	}
	return nil
}

//nolint:unused
func (r *resiliencies) recv(context.Context) (*loader.Event[resiliencyapi.Resiliency], error) {
	event, err := r.Recv()
	if err != nil {
		return nil, err
	}

	var resiliency resiliencyapi.Resiliency
	if err := json.Unmarshal(event.GetResiliency(), &resiliency); err != nil {
		return nil, fmt.Errorf("failed to deserialize resiliency: %w", err)
	}

	return &loader.Event[resiliencyapi.Resiliency]{
		Resource: resiliency,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (r *resiliencies) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns string) error {
	stream, err := opclient.ResiliencyUpdate(ctx, &operatorpb.ResiliencyUpdateRequest{
		Namespace: ns,
	})
	if err != nil {
		return err
	}

	r.Operator_ResiliencyUpdateClient = stream
	return nil
}
