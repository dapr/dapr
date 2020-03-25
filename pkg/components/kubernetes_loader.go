// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cenkalti/backoff"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/golang/protobuf/ptypes/empty"
)

const maxRetryTime = time.Second * 30

var log = logger.NewLogger("dapr.runtime.components")

// KubernetesComponents loads components in a kubernetes environment
type KubernetesComponents struct {
	config config.KubernetesConfig
}

// NewKubernetesComponents returns a new kubernetes loader
func NewKubernetesComponents(configuration config.KubernetesConfig) *KubernetesComponents {
	return &KubernetesComponents{
		config: configuration,
	}
}

// LoadComponents returns components from a given control plane address
func (k *KubernetesComponents) LoadComponents() ([]components_v1alpha1.Component, error) {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = maxRetryTime

	var components []components_v1alpha1.Component

	client, conn, err := client.GetOperatorClient(k.config.ControlPlaneAddress)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	err = backoff.Retry(func() error {
		resp, getErr := client.GetComponents(context.Background(), &empty.Empty{})
		if getErr != nil {
			return getErr
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
		return nil
	}, b)
	return components, err
}
