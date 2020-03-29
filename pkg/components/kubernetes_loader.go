// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"context"
	"encoding/json"
	"time"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/proto/operator"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
)

const maxRetryTime = time.Second * 30

var log = logger.NewLogger("dapr.runtime.components")

// KubernetesComponents loads components in a kubernetes environment
type KubernetesComponents struct {
	config config.KubernetesConfig
	client operator.OperatorClient
}

// NewKubernetesComponents returns a new kubernetes loader
func NewKubernetesComponents(configuration config.KubernetesConfig, operatorClient operator.OperatorClient) *KubernetesComponents {
	return &KubernetesComponents{
		config: configuration,
		client: operatorClient,
	}
}

// LoadComponents returns components from a given control plane address
func (k *KubernetesComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	var components []components_v1alpha1.Component
	resp, err := k.client.GetComponents(context.Background(), &empty.Empty{}, grpc_retry.WithMax(100), grpc_retry.WithPerRetryTimeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	comps := resp.GetComponents()

	for _, c := range comps {
		var component components_v1alpha1.Component
		serErr := json.Unmarshal(c.Value, &component)
		if serErr != nil {
			log.Warnf("error deserializing component: %s", serErr)
			continue
		}
		components = append(components, component)
	}
	return components, err
}
