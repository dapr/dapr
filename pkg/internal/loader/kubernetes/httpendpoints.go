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

	endpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/internal/loader"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type httpendpoints struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewHTTPEndpoints returns a new Kubernetes loader.
func NewHTTPEndpoints(opts Options) loader.Loader[endpointapi.HTTPEndpoint] {
	return &httpendpoints{
		config:    opts.Config,
		client:    opts.Client,
		namespace: opts.Namespace,
		podName:   opts.PodName,
	}
}

// Load loads dapr components from a given directory.
func (h *httpendpoints) Load(ctx context.Context) ([]endpointapi.HTTPEndpoint, error) {
	resp, err := h.client.ListHTTPEndpoints(ctx, &operatorv1pb.ListHTTPEndpointsRequest{
		Namespace: h.namespace,
	}, grpcretry.WithMax(operatorMaxRetries), grpcretry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		log.Errorf("Error listing http endpoints: %v", err)
		return nil, err
	}

	ends := resp.GetHttpEndpoints()
	if ends == nil {
		log.Debug("No http endpoints found")
		return nil, nil
	}

	endpoints := make([]endpointapi.HTTPEndpoint, len(ends))
	for i, e := range ends {
		var endpoint endpointapi.HTTPEndpoint
		if err := json.Unmarshal(e, &endpoint); err != nil {
			return nil, fmt.Errorf("error deserializing http endpoint: %s", err)
		}

		endpoints[i] = endpoint
	}

	return endpoints, nil
}
