/*
Copyright 2026 The Dapr Authors
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

package runtime

import (
	"context"
	"fmt"

	endpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/modes"
)

func (a *DaprRuntime) loadHTTPEndpoints(ctx context.Context) error {
	var l loader.Loader[endpointapi.HTTPEndpoint]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		l = kubernetes.NewHTTPEndpoints(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
		})
	case modes.StandaloneMode:
		l = disk.NewHTTPEndpoints(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading endpoints…")

	endpoints, err := l.Load(ctx)
	if err != nil {
		return err
	}

	authorizedHTTPEndpoints := a.authz.GetAuthorizedObjects(endpoints, a.authz.IsObjectAuthorized).([]endpointapi.HTTPEndpoint)

	for _, e := range authorizedHTTPEndpoints {
		log.Infof("Found http endpoint: %s", e.Name)

		if a.processor.AddPendingEndpoint(ctx, e) == nil {
			return nil
		}
	}

	return nil
}

func (a *DaprRuntime) flushOutstandingHTTPEndpoints(ctx context.Context) error {
	log.Info("Waiting for all outstanding http endpoints to be processed…")
	// HTTP endpoints, MCP servers and components all share the root loop;
	// Flush is a Barrier that resolves once every in-flight init has
	// completed.
	if err := a.processor.Flush(ctx); err != nil {
		return fmt.Errorf("flush http endpoints: %w", err)
	}
	log.Info("All outstanding http endpoints processed")
	return nil
}
