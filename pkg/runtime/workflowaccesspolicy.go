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

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/internal/loader/validate"
	"github.com/dapr/dapr/pkg/modes"
)

func (a *DaprRuntime) workflowAccessPolicyLoader() loader.Loader[wfaclapi.WorkflowAccessPolicy] {
	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		return kubernetes.NewWorkflowAccessPolicies(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
		}, a.runtimeConfig.id)
	case modes.StandaloneMode:
		return disk.NewWorkflowAccessPolicies(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}
}

func (a *DaprRuntime) loadWorkflowAccessPolicies(ctx context.Context) error {
	l := a.workflowAccessPolicyLoader()
	if l == nil {
		return nil
	}

	policies, err := l.Load(ctx)
	if err != nil {
		return err
	}

	valid := policies[:0]
	for _, p := range policies {
		if err := validate.WorkflowAccessPolicy(ctx, &p); err != nil {
			log.Warnf("WorkflowAccessPolicy %q failed validation, skipping: %s", p.Name, err)
			continue
		}
		a.compStore.AddWorkflowAccessPolicy(p)
		valid = append(valid, p)
	}

	compiled := workflowacl.Compile(valid)
	a.workflowAccessPolicies.Store(compiled)

	if compiled != nil {
		log.Infof("Loaded %d workflow access policy resource(s)", len(valid))
	}

	return nil
}
