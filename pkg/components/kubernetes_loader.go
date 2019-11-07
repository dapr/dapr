// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import (
	"encoding/json"
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/cenkalti/backoff"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/valyala/fasthttp"
)

const maxRetryTime = time.Second * 30

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

	url := fmt.Sprintf("%s/components", k.config.ControlPlaneAddress)
	var components []components_v1alpha1.Component

	err := backoff.Retry(func() error {
		body, err := requestControlPlane(url)
		if err != nil {
			return err
		}
		err = json.Unmarshal(body, &components)
		if err != nil {
			return nil
		}
		return nil
	}, b)

	return components, err
}

// Retry mechanism when requesting the Operator API
func requestControlPlane(url string) ([]byte, error) {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{
		ReadTimeout: time.Second * 10,
	}
	err := client.Do(req, resp)
	if err != nil {
		// Request failed, try again
		log.Info("Retrying getting components")
		return nil, err
	}

	body := resp.Body()
	return body, nil
}
