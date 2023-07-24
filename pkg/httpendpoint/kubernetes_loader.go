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

package httpendpoint

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dapr/kit/logger"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	httpEndpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"

	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

var log = logger.NewLogger("dapr.runtime.httpendpoints")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
)

// KubernetesHTTPEndpoints loads http endpoints in a kubernetes environment.
type KubernetesHTTPEndpoints struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewKubernetesHTTPEndpoints returns a new kubernetes loader.
func NewKubernetesHTTPEndpoints(configuration config.KubernetesConfig, namespace string, operatorClient operatorv1pb.OperatorClient, podName string) *KubernetesHTTPEndpoints {
	return &KubernetesHTTPEndpoints{
		config:    configuration,
		client:    operatorClient,
		namespace: namespace,
		podName:   podName,
	}
}

// LoadHTTPEndpoints returns HTTP endpoints from a given control plane address.
func (k *KubernetesHTTPEndpoints) LoadHTTPEndpoints() ([]httpEndpointsV1alpha1.HTTPEndpoint, error) {
	resp, err := k.client.ListHTTPEndpoints(context.Background(), &operatorv1pb.ListHTTPEndpointsRequest{
		Namespace: k.namespace,
	}, grpcRetry.WithMax(operatorMaxRetries), grpcRetry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		log.Errorf("Error listing http endpoints: %v", err)
		return nil, err
	}

	ends := resp.GetHttpEndpoints()
	if ends == nil {
		log.Debug("No http endpoints found")
		return nil, nil
	}

	endpoints := []httpEndpointsV1alpha1.HTTPEndpoint{}
	for _, e := range ends {
		var endpoint httpEndpointsV1alpha1.HTTPEndpoint
		endpoint.Spec = httpEndpointsV1alpha1.HTTPEndpointSpec{}
		err := json.Unmarshal(e, &endpoint)
		if err != nil {
			log.Warnf("error deserializing http endpoint: %s", err)
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}
