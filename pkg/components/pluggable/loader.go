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

package pluggable

import (
	"context"
	"encoding/json"
	"os"
	"time"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"

	"github.com/dapr/dapr/pkg/components"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
)

// toPluggable converts the pluggable component manifest to a pluggable component businessmodel struct.
func toPluggable(comp componentsV1alpha1.PluggableComponent) components.Pluggable {
	return components.Pluggable{
		Name:    comp.GetObjectMeta().GetName(),
		Type:    components.PluggableType(comp.Spec.Type),
		Version: comp.Spec.Version,
	}
}

// mapComponents maps from pluggablecomponent manifest to pluggable component obj.
func mapComponents(comps []componentsV1alpha1.PluggableComponent) []components.Pluggable {
	pluggableComponents := make([]components.Pluggable, len(comps))
	for idx, component := range comps {
		pluggableComponents[idx] = toPluggable(component)
	}
	return pluggableComponents
}

// newFromPath creates a disk manifest loader from the given path.
func newFromPath(pluggableComponentsPath string) components.ManifestLoader[componentsV1alpha1.PluggableComponent] {
	return components.NewDiskManifestLoader(pluggableComponentsPath, func() componentsV1alpha1.PluggableComponent {
		var comp componentsV1alpha1.PluggableComponent
		comp.Spec = componentsV1alpha1.PluggableComponentSpec{}
		return comp
	})
}

// LoadFromDisk loads PluggableComponents from the given path.
func LoadFromDisk(pluggableComponentsPath string) ([]components.Pluggable, error) {
	comp, err := newFromPath(pluggableComponentsPath).Load()
	if os.IsNotExist(err) {
		return make([]components.Pluggable, 0), nil
	}
	return mapComponents(comp), err
}

const (
	callTimeout = time.Second * 5
	maxRetries  = 100
)

// LoadFromKuberentes load pluggable components when running in a kubernetes mode.
func LoadFromKubernetes(namespace, podName string, client operatorv1pb.OperatorClient) ([]components.Pluggable, error) {
	resp, err := client.ListPluggableComponents(context.Background(), &operatorv1pb.ListPluggableComponentsRequest{
		Namespace: namespace,
		PodName:   podName,
	}, grpcRetry.WithMax(maxRetries), grpcRetry.WithPerRetryTimeout(callTimeout))
	if err != nil {
		return nil, err
	}

	comps := resp.GetPluggableComponents()

	pluggableComponents := []components.Pluggable{}
	for _, c := range comps {
		var component componentsV1alpha1.PluggableComponent
		component.Spec = componentsV1alpha1.PluggableComponentSpec{}
		err := json.Unmarshal(c, &component)
		if err != nil {
			log.Warnf("error deserializing pluggable component: %s", err)
			continue
		}
		pluggableComponents = append(pluggableComponents, toPluggable(component))
	}
	return pluggableComponents, nil
}
