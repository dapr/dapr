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

package components

import (
	"context"
	"encoding/json"
	"time"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	"github.com/dapr/kit/logger"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

var log = logger.NewLogger("dapr.runtime.components")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
)

// KubernetesComponents loads components in a kubernetes environment.
type KubernetesComponents struct {
	config    config.KubernetesConfig
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewKubernetesComponents returns a new kubernetes loader.
func NewKubernetesComponents(configuration config.KubernetesConfig, namespace string, operatorClient operatorv1pb.OperatorClient, podName string) *KubernetesComponents {
	return &KubernetesComponents{
		config:    configuration,
		client:    operatorClient,
		namespace: namespace,
		podName:   podName,
	}
}

// LoadComponents returns components from a given control plane address.
func (k *KubernetesComponents) LoadComponents() ([]componentsV1alpha1.Component, error) {
	resp, err := k.client.ListComponents(context.Background(), &operatorv1pb.ListComponentsRequest{
		Namespace: k.namespace,
		PodName:   k.podName,
	}, grpcRetry.WithMax(operatorMaxRetries), grpcRetry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	comps := resp.GetComponents()

	components := []componentsV1alpha1.Component{}
	for _, c := range comps {
		var component componentsV1alpha1.Component
		component.Spec = componentsV1alpha1.ComponentSpec{}
		err := json.Unmarshal(c, &component)
		if err != nil {
			log.Warnf("error deserializing component: %s", err)
			continue
		}
		components = append(components, component)
	}
	return components, nil
}
