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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	resolvedHeaders, err := processConfigurationSecrets(ctx, &config, in.GetNamespace(), a.Client)
	if err != nil {
		log.Warnf("error processing configuration %s secrets from pod %s/%s: %s", config.Name, in.GetNamespace(), in.GetPodName(), err)
		return nil, fmt.Errorf("error processing configuration secrets: %w", err)
	}

	b, err := marshalConfigurationWithResolvedOtel(&config, resolvedHeaders)
	if err != nil {
		return nil, fmt.Errorf("error marshalling configuration: %w", err)
	}
	return &operatorv1pb.GetConfigurationResponse{
		Configuration: b,
	}, nil
}

// marshalConfigurationWithResolvedOtel marshals the configuration, converting
// CRD NameValuePair headers to simple "key=value" strings and metav1.Duration
// timeout to time.Duration that the runtime internal config expects.
func marshalConfigurationWithResolvedOtel(config *configurationapi.Configuration, resolvedHeaders []string) ([]byte, error) {
	b, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	if config.Spec.TracingSpec == nil || config.Spec.TracingSpec.Otel == nil {
		return b, nil
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}

	spec, ok := raw["spec"].(map[string]interface{})
	if !ok {
		return b, nil
	}

	tracing, ok := spec["tracing"].(map[string]interface{})
	if !ok {
		return b, nil
	}

	otel, ok := tracing["otel"].(map[string]interface{})
	if !ok {
		return b, nil
	}

	// Replace headers with resolved []string format
	if len(resolvedHeaders) > 0 {
		otel["headers"] = resolvedHeaders
	} else {
		delete(otel, "headers")
	}

	// Convert metav1.Duration timeout to time.Duration
	if config.Spec.TracingSpec.Otel.Timeout != nil {
		otel["timeout"] = config.Spec.TracingSpec.Otel.Timeout.Duration.Nanoseconds()
	}

	return json.Marshal(raw)
}

// processConfigurationSecrets resolves secret references in configuration
// headers and returns the headers as simple "key=value" strings.
func processConfigurationSecrets(ctx context.Context, config *configurationapi.Configuration, namespace string, kubeClient client.Client) ([]string, error) {
	if config.Spec.TracingSpec == nil || config.Spec.TracingSpec.Otel == nil {
		return nil, nil
	}

	otel := config.Spec.TracingSpec.Otel
	if len(otel.Headers) == 0 {
		return nil, nil
	}

	resolved := make([]string, 0, len(otel.Headers))

	for _, header := range otel.Headers {
		var value string

		if header.SecretKeyRef.Name != "" {
			var secret corev1.Secret
			err := kubeClient.Get(ctx, types.NamespacedName{
				Name:      header.SecretKeyRef.Name,
				Namespace: namespace,
			}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get secret %s for header %s: %w", header.SecretKeyRef.Name, header.Name, err)
			}

			key := header.SecretKeyRef.Key
			if key == "" {
				return nil, fmt.Errorf("secret key is required for header %s", header.Name)
			}

			val, ok := secret.Data[key]
			if !ok {
				return nil, fmt.Errorf("key %s not found in secret %s", key, header.SecretKeyRef.Name)
			}

			value = string(val)
		} else if header.HasValue() {
			value = header.Value.String()
		}

		resolved = append(resolved, header.Name+"="+value)
	}

	return resolved, nil
}
