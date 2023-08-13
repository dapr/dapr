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

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type component struct {
	operatorpb.Operator_ComponentUpdateClient
}

func (c *component) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error) {
	resp, err := opclient.ListComponents(ctx, &operatorpb.ListComponentsRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetComponents(), nil
}

func (c *component) close() error {
	if c.Operator_ComponentUpdateClient != nil {
		return c.Operator_ComponentUpdateClient.CloseSend()
	}
	return nil
}

func (c *component) recv() (*loader.Event[compapi.Component], error) {
	event, err := c.Operator_ComponentUpdateClient.Recv()
	if err != nil {
		return nil, err
	}

	var component compapi.Component
	if err := json.Unmarshal(event.Component, &component); err != nil {
		return nil, fmt.Errorf("failed to deserializing component: %s", err)
	}

	return &loader.Event[compapi.Component]{
		Resource: component,
		Type:     event.Type,
	}, nil
}

func (c *component) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) error {
	stream, err := opclient.ComponentUpdate(ctx, &operatorpb.ComponentUpdateRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return err
	}

	c.Operator_ComponentUpdateClient = stream
	return nil
}
