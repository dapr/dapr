/*
Copyright 2021 The Dapr Authors
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

package externalendpoint

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dapr/kit/logger"

	externalendpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/externalHTTPEndpoint/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
)

var log = logger.NewLogger("dapr.runtime.externalendpoints")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
)

// KubernetesExternalHTTPEndpoints loads external http endpoints in a kubernetes environment.
type KubernetesExternalHTTPEndpoints struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewKubernetesExternalHTTPEndpoints returns a new kubernetes loader.
func NewKubernetesExternalHTTPEndpoints(configuration config.KubernetesConfig, namespace string, operatorClient operatorv1pb.OperatorClient, podName string) *KubernetesExternalHTTPEndpoints {
	return &KubernetesExternalHTTPEndpoints{
		config:    configuration,
		client:    operatorClient,
		namespace: namespace,
		podName:   podName,
	}
}

// LoadExternalHTTPEndpoints returns external HTTP endpoints from a given control plane address.
func (k *KubernetesExternalHTTPEndpoints) LoadExternalHTTPEndpoints() ([]externalendpointsV1alpha1.ExternalHTTPEndpoint, error) {
	log.Info("in LoadExternalHTTPEndpoints()")
	resp, err := k.client.ListExternalHTTPEndpoints(context.Background(), &operatorv1pb.ListExternalHTTPEndpointsRequest{
		Namespace: k.namespace,
	}, grpcRetry.WithMax(operatorMaxRetries), grpcRetry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		log.Errorf("Error listing external http endpoints: %v", err)
		return nil, err
	}

	if resp.GetExternalhttpendpoints() == nil {
		log.Debug("No external http endpoints found")
		return nil, nil
	}

	ends := resp.GetExternalhttpendpoints()

	endpoints := []externalendpointsV1alpha1.ExternalHTTPEndpoint{}
	for _, e := range ends {
		var endpoint externalendpointsV1alpha1.ExternalHTTPEndpoint
		endpoint.Spec = externalendpointsV1alpha1.ExternalHTTPEndpointSpec{}
		err := json.Unmarshal(e, &endpoint)
		if err != nil {
			log.Warnf("error deserializing external http endpoint: %s", err)
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}
