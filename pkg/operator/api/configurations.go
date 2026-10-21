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
	"encoding/csv"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorconsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatormeta "github.com/dapr/dapr/pkg/operator/meta"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// GetConfiguration returns a Dapr configuration.
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	id, err := authz.Request(ctx, in.GetNamespace())
	if err != nil {
		return nil, err
	}

	// The sidecar is not trusted to self-report which configuration it is
	// assigned; resolve it from the pod annotation.
	configs, err := a.appConfigurations(ctx, in.GetNamespace(), id.AppID())
	if err != nil {
		// Unavailable is retried by daprd's operator client, so a transient
		// failure does not fail sidecar startup.
		return nil, status.Errorf(codes.Unavailable, "failed to resolve assigned configuration for app %s in namespace %s: %s", id.AppID(), in.GetNamespace(), err)
	}
	// During a rollout that changes dapr.io/config, old and new pods share the
	// app ID with different configurations, so any of them is allowed.
	if !slices.Contains(configs, in.GetName()) {
		return nil, status.Errorf(codes.PermissionDenied, "configuration %s/%s is not assigned to app %s", in.GetNamespace(), in.GetName(), id.AppID())
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var config configurationapi.Configuration
	if err = a.Client.Get(ctx, key, &config); err != nil {
		return nil, fmt.Errorf("error getting configuration %s/%s: %w", in.GetNamespace(), in.GetName(), err)
	}

	if err = processConfigurationSecrets(ctx, &config, in.GetNamespace(), a.Client); err != nil {
		log.Warnf("error processing configuration %s secrets in namespace %s: %s", config.Name, in.GetNamespace(), err)
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

// appConfigurations returns the dapr.io/config annotation of every Dapr pod
// of the app in the namespace, skipping pods without one. Pods are read from
// the API server, selected by the app ID label the injector sets on every pod.
// A sidecar only runs once its pod exists, so the read is authoritative.
func (a *apiServer) appConfigurations(ctx context.Context, namespace, appID string) ([]string, error) {
	var pods metav1.PartialObjectMetadataList
	pods.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	if err := a.podReader.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels{injectorconsts.SidecarAppIDLabel: appID}); err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}

	var configs []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !operatormeta.IsAnnotatedForDapr(pod.GetAnnotations()) || podAppID(pod) != appID {
			continue
		}
		if config := daprdConfigName(pod.GetAnnotations()[annotations.KeyConfig]); config != "" {
			configs = append(configs, config)
		}
	}

	return configs, nil
}

// daprdConfigName returns the configuration daprd loads for a dapr.io/config
// annotation. The injector passes the annotation verbatim to daprd's --config
// flag, which parses it as a CSV record, and daprd in Kubernetes mode only
// loads the first entry.
func daprdConfigName(annotation string) string {
	names, err := csv.NewReader(strings.NewReader(annotation)).Read()
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0]
}

// podAppID returns the Dapr app ID of a pod, mirroring the injector: the
// dapr.io/app-id annotation, falling back to the pod name.
func podAppID(pod metav1.Object) string {
	if id := pod.GetAnnotations()[annotations.KeyAppID]; id != "" {
		return id
	}
	return pod.GetName()
}
