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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/modes"
)

// loadComponents loads every component manifest the runtime is configured
// for, splits them into secret stores (first pass) and everything else
// (second pass), and waits for each Init result before continuing. Returning
// a non-nil error here triggers daprd shutdown via the runner manager.
func (a *DaprRuntime) loadComponents(ctx context.Context) error {
	var l loader.Loader[compapi.Component]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		l = kubernetes.NewComponents(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
		})
	case modes.StandaloneMode:
		l = disk.NewComponents(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading components…")

	comps, err := l.Load(ctx)
	if err != nil {
		return err
	}

	authorizedComps := a.authz.GetAuthorizedObjects(comps, a.authz.IsObjectAuthorized).([]compapi.Component)

	// Iterate through the list twice. First, we look for secret stores and
	// load those, then all other components. This avoids stashing components
	// behind unready secret store dependencies during bootstrap.
	for _, comp := range authorizedComps {
		if strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())

			if err := a.initComponentBlocking(ctx, comp); err != nil {
				return err
			}
		}
	}

	for _, comp := range authorizedComps {
		if !strings.HasPrefix(comp.Spec.Type, string(components.CategorySecretStore)+".") {
			log.Debug("Found component: " + comp.LogName())

			if err := a.initComponentBlocking(ctx, comp); err != nil {
				return err
			}
		}
	}

	return nil
}

// initComponentBlocking enqueues the component for init and waits for the
// result. Returns nil if the component's spec sets IgnoreErrors, otherwise
// surfaces init errors so the runtime can shut down gracefully.
func (a *DaprRuntime) initComponentBlocking(ctx context.Context, comp compapi.Component) error {
	ch := a.processor.AddPendingComponent(ctx, comp)
	if ch == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-ch:
		if err == nil {
			return nil
		}
		err = fmt.Errorf("process component %s error: %s", comp.Name, err)
		if comp.Spec.IgnoreErrors {
			log.Errorf("Ignoring error processing component: %s", err)
			return nil
		}
		log.Warnf("Error processing component, daprd will exit gracefully: %s", err)
		return err
	}
}

func (a *DaprRuntime) flushOutstandingComponents(ctx context.Context) error {
	log.Info("Waiting for all outstanding components to be processed…")
	if err := a.processor.Flush(ctx); err != nil {
		return fmt.Errorf("flush components: %w", err)
	}
	log.Info("All outstanding components processed")
	return nil
}

// appendBuiltinSecretStore preloads the Kubernetes built-in secret store
// when running in Kubernetes mode unless explicitly disabled.
func (a *DaprRuntime) appendBuiltinSecretStore(ctx context.Context) {
	if a.runtimeConfig.disableBuiltinK8sSecretStore {
		return
	}

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		a.processor.AddPendingComponent(ctx, compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: components.FirstStableVersion,
			},
		})
	}
}
