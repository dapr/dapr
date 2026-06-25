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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	operatormeta "github.com/dapr/dapr/pkg/operator/meta"
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

// ConfigurationUpdate handles configuration update streaming for a connected client.
// Each client connection gets its own client loop that watches the informer
// and sends updates over the gRPC stream.
func (a *apiServer) ConfigurationUpdate(in *operatorv1pb.ConfigurationUpdateRequest, srv operatorv1pb.Operator_ConfigurationUpdateServer) error { //nolint:nosnakecase
	if a.closed.Load() {
		return errors.New("server is closed")
	}

	log.Info("sidecar connected for configuration updates")

	ctx := srv.Context()

	// Verify authorization and resolve the connecting app's identity.
	id, err := authz.Request(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	// Determine, server side, which configuration is assigned to the connecting
	// app from its pod's dapr.io/config annotation. The sidecar is not trusted to
	// self-report this. Only updates to that configuration are streamed, so the
	// sidecar is not restarted by changes to configurations that do not belong to
	// it. This is resolved before registering the informer watcher so a slow pod
	// cache lookup cannot back up the watcher's event channel.
	assigned, err := a.appAssignedConfiguration(ctx, in.GetNamespace(), id.AppID())
	if err != nil {
		return err
	}
	if assigned == "" {
		log.Debugf("app %s in namespace %s has no assigned configuration; no configuration updates will be streamed", id.AppID(), in.GetNamespace())
	} else {
		log.Debugf("streaming updates for configuration %q to app %s in namespace %s", assigned, id.AppID(), in.GetNamespace())
	}

	// Verify authorization via informer's WatchUpdates, which checks SPIFFE ID
	ch, cancel, err := a.configInformer.WatchUpdates(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	stream, err := sender.New(srv)
	if err != nil {
		cancel()
		return err
	}

	// Create a client for this connection
	client := loopsclient.New(loopsclient.Options[configurationapi.Configuration]{
		EventCh:        ch,
		CancelWatch:    cancel,
		Stream:         stream,
		Namespace:      in.GetNamespace(),
		KubeClient:     a.Client,
		ProcessSecrets: processConfigurationSecrets,
		Filter: func(c configurationapi.Configuration) bool {
			return c.GetName() == assigned
		},
	})
	defer client.CacheLoop()

	// Run the client - this will block until context is done or event channel closes
	if err := client.Run(ctx); err != nil {
		log.Warnf("configuration client loop ended with error: %s", err)
	}

	return nil
}

// appAssignedConfiguration returns the name of the configuration assigned to the
// given app, as declared by the dapr.io/config annotation on the app's pod. It
// reads pod metadata only (no spec/status) from a dedicated metadata cache. It
// returns an empty string when the app has no pod with a configuration assigned
// (in which case no configuration updates should be streamed to it).
func (a *apiServer) appAssignedConfiguration(ctx context.Context, namespace, appID string) (string, error) {
	var pods metav1.PartialObjectMetadataList
	pods.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	if err := a.podReader.List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("error listing pods to resolve configuration for app %s: %w", appID, err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if !operatormeta.IsAnnotatedForDapr(pod.GetAnnotations()) {
			continue
		}
		if podAppID(pod) == appID {
			return pod.GetAnnotations()[annotations.KeyConfig], nil
		}
	}

	return "", nil
}

// podAppID returns the Dapr app ID of a pod, mirroring the injector: the
// dapr.io/app-id annotation, falling back to the pod name.
func podAppID(pod metav1.Object) string {
	if id := pod.GetAnnotations()[annotations.KeyAppID]; id != "" {
		return id
	}
	return pod.GetName()
}
