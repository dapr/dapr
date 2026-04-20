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

package reconciler

import (
	"context"

	"k8s.io/utils/clock"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/internal/loader/validate"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

// PolicyRecompiler is a callback that atomically replaces the compiled policies.
type PolicyRecompiler func(policies *workflowacl.CompiledPolicies)

// WorkflowAccessPolicyOptions holds options for creating a WorkflowAccessPolicy reconciler.
type WorkflowAccessPolicyOptions struct {
	AppID      string
	Namespace  string
	Loader     loader.Interface
	CompStore  *compstore.ComponentStore
	Recompiler PolicyRecompiler
	Healthz    healthz.Healthz
}

type workflowAccessPolicies struct {
	appID      string
	namespace  string
	store      *compstore.ComponentStore
	recompiler PolicyRecompiler
	loader.Loader[wfaclapi.WorkflowAccessPolicy]
}

func NewWorkflowAccessPolicies(opts WorkflowAccessPolicyOptions) *Reconciler[wfaclapi.WorkflowAccessPolicy] {
	r := &Reconciler[wfaclapi.WorkflowAccessPolicy]{
		kind:    "WorkflowAccessPolicy",
		htarget: opts.Healthz.AddTarget("workflowaccesspolicy-reconciler"),
		clock:   clock.RealClock{},
		manager: &workflowAccessPolicies{
			Loader:     opts.Loader.WorkflowAccessPolicies(),
			appID:      opts.AppID,
			namespace:  opts.Namespace,
			store:      opts.CompStore,
			recompiler: opts.Recompiler,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// recompileAll fetches all policies from the compstore, filters by app scope,
// compiles them, and atomically swaps them on the gRPC API.
//
//nolint:unused
func (w *workflowAccessPolicies) recompileAll() {
	all := w.store.ListWorkflowAccessPolicies()
	var scoped []wfaclapi.WorkflowAccessPolicy
	for _, p := range all {
		if p.IsAppScoped(w.appID) {
			scoped = append(scoped, p)
		}
	}
	compiled := workflowacl.Compile(scoped, w.namespace)
	w.recompiler(compiled)
	log.Infof("Recompiled %d workflow access policy resource(s) (of %d total)", len(scoped), len(all))
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (w *workflowAccessPolicies) update(_ context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	if err := validate.WorkflowAccessPolicy(&policy); err != nil {
		log.Warnf("WorkflowAccessPolicy %q failed validation, skipping: %s", policy.Name, err)
		return
	}

	w.store.AddWorkflowAccessPolicy(policy)
	w.recompileAll()
}

//nolint:unused
func (w *workflowAccessPolicies) delete(_ context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	w.store.DeleteWorkflowAccessPolicy(policy.Name)
	w.recompileAll()
}
