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

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type configurations struct {
	operatorpb.Operator_ConfigurationUpdateClient
}

// The go linter does not yet understand that these functions are being used by
// the generic operator.
//
//nolint:unused
func (c *configurations) list(ctx context.Context, opclient operatorpb.OperatorClient, ns string) ([][]byte, error) {
	// Configuration doesn't have a List RPC, so we return empty list.
	// The initial configuration is loaded at startup, and updates come via streaming.
	return nil, nil
}

//nolint:unused
func (c *configurations) close() error {
	if c.Operator_ConfigurationUpdateClient != nil {
		return c.CloseSend()
	}
	return nil
}

//nolint:unused
func (c *configurations) recv(context.Context) (*loader.Event[configapi.Configuration], error) {
	event, err := c.Recv()
	if err != nil {
		return nil, err
	}

	var config configapi.Configuration
	if err := json.Unmarshal(event.GetConfiguration(), &config); err != nil {
		return nil, fmt.Errorf("failed to deserialize configuration: %w", err)
	}

	return &loader.Event[configapi.Configuration]{
		Resource: config,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (c *configurations) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns string) error {
	stream, err := opclient.ConfigurationUpdate(ctx, &operatorpb.ConfigurationUpdateRequest{
		Namespace: ns,
	})
	if err != nil {
		return err
	}

	c.Operator_ConfigurationUpdateClient = stream
	return nil
}
