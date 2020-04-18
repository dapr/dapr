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
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
)

var log = logger.NewLogger("dapr.runtime.components")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
)

// KubernetesComponents loads components in a kubernetes environment
type KubernetesComponents struct {
	config config.KubernetesConfig
	client operatorv1pb.OperatorClient
}

// NewKubernetesComponents returns a new kubernetes loader
func NewKubernetesComponents(configuration config.KubernetesConfig, operatorClient operatorv1pb.OperatorClient) *KubernetesComponents {
	return &KubernetesComponents{
		config: configuration,
		client: operatorClient,
	}
}

// LoadComponents returns components from a given control plane address
func (k *KubernetesComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	resp, err := k.client.GetComponents(context.Background(), &empty.Empty{}, grpc_retry.WithMax(operatorMaxRetries), grpc_retry.WithPerRetryTimeout(operatorCallTimeout))
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
