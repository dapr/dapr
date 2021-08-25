// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"context"
	"encoding/json"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	"github.com/dapr/kit/logger"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
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
}

// NewKubernetesComponents returns a new kubernetes loader.
func NewKubernetesComponents(configuration config.KubernetesConfig, namespace string, operatorClient operatorv1pb.OperatorClient) *KubernetesComponents {
	return &KubernetesComponents{
		config:    configuration,
		client:    operatorClient,
		namespace: namespace,
	}
}

// LoadComponents returns components from a given control plane address.
func (k *KubernetesComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	resp, err := k.client.ListComponents(context.Background(), &operatorv1pb.ListComponentsRequest{
		Namespace: k.namespace,
	}, grpc_retry.WithMax(operatorMaxRetries), grpc_retry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	comps := resp.GetComponents()

	components := []components_v1alpha1.Component{}
	for _, c := range comps {
		var component components_v1alpha1.Component
		component.Spec = components_v1alpha1.ComponentSpec{}
		err := json.Unmarshal(c, &component)
		if err != nil {
			log.Warnf("error deserializing component: %s", err)
			continue
		}
		components = append(components, component)
	}
	return components, nil
}
