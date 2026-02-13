/*
Copyright 2024 The Dapr Authors
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

package api

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// GetConfiguration returns a Dapr configuration.
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var config configurationapi.Configuration
	if err := a.Client.Get(ctx, key, &config); err != nil {
		return nil, fmt.Errorf("error getting configuration %s/%s: %w", in.GetNamespace(), in.GetName(), err)
	}

	if err := processConfigurationSecrets(ctx, &config, in.GetNamespace(), a.Client); err != nil {
		log.Warnf("error processing configuration %s secrets from pod %s/%s: %s", config.Name, in.GetNamespace(), in.GetPodName(), err)
		return nil, fmt.Errorf("error processing configuration secrets: %w", err)
	}

	b, err := json.Marshal(&config)
	if err != nil {
		return nil, fmt.Errorf("error marshalling configuration: %w", err)
	}
	return &operatorv1pb.GetConfigurationResponse{
		Configuration: b,
	}, nil
}

// processConfigurationSecrets resolves secret references in configuration
func processConfigurationSecrets(ctx context.Context, config *configurationapi.Configuration, namespace string, kubeClient client.Client) error {
	if config.Spec.TracingSpec == nil || config.Spec.TracingSpec.Otel == nil {
		return nil
	}

	otel := config.Spec.TracingSpec.Otel
	for i, header := range otel.Headers {
		if header.SecretKeyRef.Name == "" {
			continue
		}

		key := header.SecretKeyRef.Key
		if key == "" {
			return fmt.Errorf("secret key is required for header %s", header.Name)
		}

		var secret corev1.Secret
		err := kubeClient.Get(ctx, types.NamespacedName{
			Name:      header.SecretKeyRef.Name,
			Namespace: namespace,
		}, &secret)
		if err != nil {
			return fmt.Errorf("failed to get secret %s for header %s: %w", header.SecretKeyRef.Name, header.Name, err)
		}

		val, ok := secret.Data[key]
		if !ok {
			return fmt.Errorf("key %s not found in secret %s", key, header.SecretKeyRef.Name)
		}

		jsonVal, err := json.Marshal(string(val))
		if err != nil {
			return err
		}
		otel.Headers[i].Value = commonapi.DynamicValue{
			JSON: apiextensionsV1.JSON{Raw: jsonVal},
		}
	}

	return nil
}
