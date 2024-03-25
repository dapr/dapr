/*
Copyright 2022 The Dapr Authors
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

package service

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	daprapi "github.com/dapr/dapr/pkg/client/clientset/versioned"
)

const (
	globalConfigName = "daprsystem"
)

// globalConfig represents a subset of the Dapr configuration that is relevant to
// the sidecar injector, sourced from the global Dapr configuration. Values are
// defaulted in the event the Dapr configuration is not present.
type globalConfig struct {
	mtlsEnabled           bool
	metadataAuthorizedIDs []string
}

func getGlobalConfig(client daprapi.Interface, controlPlaneNamespace string) (*globalConfig, error) {
	config, err := client.ConfigurationV1alpha1().
		Configurations(controlPlaneNamespace).
		Get(globalConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("Dapr system configuration '%s' does not exist; using default value %t for mTLSEnabled", globalConfigName, true)
		return &globalConfig{
			mtlsEnabled: true,
		}, nil
	}

	if err != nil {
		log.Errorf("Failed to load global dapr configuration '%s' from Kubernetes: %w", globalConfigName, err)
		return nil, err
	}

	return &globalConfig{
		mtlsEnabled:           config.Spec.MTLSSpec.GetEnabled(),
		metadataAuthorizedIDs: config.Spec.MTLSSpec.MetadataAuthorizedIDs,
	}, nil
}
