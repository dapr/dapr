// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"encoding/json"
	"fmt"
	"time"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/valyala/fasthttp"
)

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
	url := fmt.Sprintf("%s/components", k.config.ControlPlaneAddress)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{
		ReadTimeout: time.Second * 10,
	}
	err := client.Do(req, resp)
	if err != nil {
		return nil, err
	}

	body := resp.Body()

	var components []components_v1alpha1.Component
	err = json.Unmarshal(body, &components)
	if err != nil {
		return nil, err
	}

	return components, nil
}
