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
	resp, err := k.client.GetComponents(context.Background(), &empty.Empty{}, grpc_retry.WithMax(100), grpc_retry.WithPerRetryTimeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	comps := resp.GetComponents()

	components := []components_v1alpha1.Component{}
	for _, c := range comps {
		var component components_v1alpha1.Component
		err := json.Unmarshal(c.Value, &component)
		if err != nil {
			log.Warnf("error deserializing component: %s", err)
			continue
		}
		components = append(components, component)
	}
	return components, nil
}
