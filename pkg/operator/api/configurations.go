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
	"errors"
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
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
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
	// assigned; resolve it from the pod annotation, as ConfigurationUpdate does.
	configs, err := a.appConfigurations(ctx, in.GetNamespace(), id.AppID())
	if err != nil {
		// Unavailable is retried by daprd, so a transient failure does not fail
		// sidecar startup.
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
		return fmt.Errorf("failed to authorize configuration update request: %w", err)
	}

	// Determine, server side, which configuration is assigned to the connecting
	// app from its pod's dapr.io/config annotation. The sidecar is not trusted to
	// self-report this. Only updates to that configuration are streamed, so the
	// sidecar is not restarted by changes to configurations that do not belong to
	// it. This is resolved before registering the informer watcher so a slow pod
	// cache lookup cannot back up the watcher's event channel.
	assigned, err := a.appAssignedConfiguration(ctx, in.GetNamespace(), id.AppID())
	if err != nil {
		return fmt.Errorf("failed to resolve assigned configuration for app %s in namespace %s: %w", id.AppID(), in.GetNamespace(), err)
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
// returns an empty string when the app has no pod with a configuration assigned
// (in which case no configuration updates should be streamed to it).
func (a *apiServer) appAssignedConfiguration(ctx context.Context, namespace, appID string) (string, error) {
	configs, err := a.appConfigurations(ctx, namespace, appID)
	if err != nil || len(configs) == 0 {
		return "", err
	}

	// Return the configuration of the first pod for this app that actually has one
	// assigned. During a rollout multiple pods can share the app ID, and some may
	// not (yet) carry a config annotation; skipping those avoids resolving to ""
	// and disabling streaming when another pod does have a config assigned.
	return configs[0], nil
}

// appConfigurations returns the configurations assigned to the app's Dapr pods.
// It reads pod metadata from a dedicated metadata cache, and only reads the API
// server when the cache fails or has no pod for the app, such as a new pod it
// has not caught up with. A cached pod is trusted, so a denied request cannot
// force an API read. A sidecar only runs once its pod exists, so the API read
// is authoritative. The injector labels every pod with its app ID, which keeps
// that read to the app's pods.
func (a *apiServer) appConfigurations(ctx context.Context, namespace, appID string) ([]string, error) {
	configs, found, err := listAppConfigurations(ctx, a.podReader, namespace, appID)
	if (err == nil && found) || a.podAPIReader == nil {
		return configs, err
	}
	configs, _, err = listAppConfigurations(ctx, a.podAPIReader, namespace, appID, client.MatchingLabels{injectorconsts.SidecarAppIDLabel: appID})
	return configs, err
}

// listAppConfigurations returns the dapr.io/config annotation of every Dapr pod
// of the app in the namespace, skipping pods without one, and whether the app
// has any Dapr pod.
func listAppConfigurations(ctx context.Context, reader client.Reader, namespace, appID string, opts ...client.ListOption) ([]string, bool, error) {
	var pods metav1.PartialObjectMetadataList
	pods.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	if err := reader.List(ctx, &pods, append(opts, client.InNamespace(namespace))...); err != nil {
		return nil, false, fmt.Errorf("error listing pods: %w", err)
	}

	var configs []string
	var found bool
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !operatormeta.IsAnnotatedForDapr(pod.GetAnnotations()) || podAppID(pod) != appID {
			continue
		}
		found = true
		if config := daprdConfigName(pod.GetAnnotations()[annotations.KeyConfig]); config != "" {
			configs = append(configs, config)
		}
	}

	return configs, found, nil
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
