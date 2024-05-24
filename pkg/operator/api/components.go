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
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/utils"
)

type ComponentUpdateEvent struct {
	Component *componentsapi.Component
	EventType operatorv1pb.ResourceEventType
}

// ComponentUpdate updates Dapr sidecars whenever a component in the cluster is modified.
// TODO: @joshvanl: Authorize pod name and namespace matches the SPIFFE ID of
// the caller.
func (a *apiServer) ComponentUpdate(in *operatorv1pb.ComponentUpdateRequest, srv operatorv1pb.Operator_ComponentUpdateServer) error { //nolint:nosnakecase
	log.Info("sidecar connected for component updates")

	ch, err := a.compInformer.WatchUpdates(srv.Context(), in.GetNamespace())
	if err != nil {
		return err
	}

	updateComponentFunc := func(ctx context.Context, t operatorv1pb.ResourceEventType, c *componentsapi.Component) {
		if c.Namespace != in.GetNamespace() {
			return
		}

		err := processComponentSecrets(ctx, c, in.GetNamespace(), a.Client)
		if err != nil {
			log.Warnf("error processing component %s secrets from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error serializing component %s (%s) from pod %s/%s: %s", c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		err = srv.Send(&operatorv1pb.ComponentUpdateEvent{
			Component: b,
			Type:      t,
		})
		if err != nil {
			log.Warnf("error updating sidecar with component %s (%s) from pod %s/%s: %s", c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName(), err)
			return
		}

		log.Debugf("updated sidecar with component %s %s (%s) from pod %s/%s", t.String(), c.GetName(), c.Spec.Type, in.GetNamespace(), in.GetPodName())
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-srv.Context().Done():
			return nil
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			updateComponentFunc(srv.Context(), event.Type, &event.Manifest)
		}
	}
}

// ListComponents returns a list of Dapr components.
func (a *apiServer) ListComponents(ctx context.Context, in *operatorv1pb.ListComponentsRequest) (*operatorv1pb.ListComponentResponse, error) {
	id, err := authz.Request(ctx, in.GetNamespace())
	if err != nil {
		return nil, err
	}

	var components componentsapi.ComponentList
	if err := a.Client.List(ctx, &components, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error getting components: %w", err)
	}
	resp := &operatorv1pb.ListComponentResponse{
		Components: [][]byte{},
	}

	appID := id.AppID()
	for i := range components.Items {
		if !(len(components.Items[i].Scopes) == 0 || utils.Contains(components.Items[i].Scopes, appID)) {
			continue
		}

		c := components.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		err := processComponentSecrets(ctx, &c, in.GetNamespace(), a.Client)
		if err != nil {
			log.Warnf("error processing component %s secrets from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			return &operatorv1pb.ListComponentResponse{}, err
		}

		b, err := json.Marshal(&c)
		if err != nil {
			log.Warnf("error marshalling component %s from pod %s/%s: %s", c.Name, in.GetNamespace(), in.GetPodName(), err)
			continue
		}
		resp.Components = append(resp.GetComponents(), b)
	}
	return resp, nil
}

func processComponentSecrets(ctx context.Context, component *componentsapi.Component, namespace string, kubeClient client.Client) error {
	for i, m := range component.Spec.Metadata {
		if m.SecretKeyRef.Name != "" && (component.Auth.SecretStore == kubernetesSecretStore || component.Auth.SecretStore == "") {
			var secret corev1.Secret

			err := kubeClient.Get(ctx, types.NamespacedName{
				Name:      m.SecretKeyRef.Name,
				Namespace: namespace,
			}, &secret)
			if err != nil {
				return err
			}

			key := m.SecretKeyRef.Key
			if key == "" {
				key = m.SecretKeyRef.Name
			}

			val, ok := secret.Data[key]
			if ok {
				enc := b64.StdEncoding.EncodeToString(val)
				jsonEnc, err := json.Marshal(enc)
				if err != nil {
					return err
				}
				component.Spec.Metadata[i].Value = commonapi.DynamicValue{
					JSON: v1.JSON{
						Raw: jsonEnc,
					},
				}
			}
		}
	}

	return nil
}

func pairNeedsSecretExtraction(ref commonapi.SecretKeyRef, auth httpendpointsapi.Auth) bool {
	return ref.Name != "" && (auth.SecretStore == kubernetesSecretStore || auth.SecretStore == "")
}

func getSecret(ctx context.Context, name, namespace string, ref commonapi.SecretKeyRef, kubeClient client.Client) (commonapi.DynamicValue, error) {
	var secret corev1.Secret

	err := kubeClient.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return commonapi.DynamicValue{}, err
	}

	key := ref.Key
	if key == "" {
		key = ref.Name
	}

	val, ok := secret.Data[key]
	if ok {
		enc := b64.StdEncoding.EncodeToString(val)
		jsonEnc, err := json.Marshal(enc)
		if err != nil {
			return commonapi.DynamicValue{}, err
		}

		return commonapi.DynamicValue{
			JSON: v1.JSON{
				Raw: jsonEnc,
			},
		}, nil
	}

	return commonapi.DynamicValue{}, nil
}
