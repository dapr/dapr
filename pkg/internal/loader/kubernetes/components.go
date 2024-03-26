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

package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"

	grpcretry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/internal/loader"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// components loads components from a Kubernetes environment.
type components struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewComponents returns a new Kubernetes loader.
func NewComponents(opts Options) loader.Loader[compapi.Component] {
	return &components{
		config:    opts.Config,
		client:    opts.Client,
		namespace: opts.Namespace,
		podName:   opts.PodName,
	}
}

// Load loads dapr components from a given directory.
func (c *components) Load(ctx context.Context) ([]compapi.Component, error) {
	resp, err := c.client.ListComponents(ctx, &operatorv1pb.ListComponentsRequest{
		Namespace: c.namespace,
		PodName:   c.podName,
	}, grpcretry.WithMax(operatorMaxRetries), grpcretry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	comps := resp.GetComponents()

	components := make([]compapi.Component, len(comps))
	for i, c := range comps {
		var component compapi.Component
		if err := json.Unmarshal(c, &component); err != nil {
			return nil, fmt.Errorf("error deserializing component: %s", err)
		}

		components[i] = component
	}
	return components, nil
}
