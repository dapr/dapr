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
	"sync"

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
	Loader     loader.Interface
	CompStore  *compstore.ComponentStore
	Recompiler PolicyRecompiler
	Healthz    healthz.Healthz
}

type workflowAccessPolicies struct {
	appID      string
	store      *compstore.ComponentStore
	recompiler PolicyRecompiler
	lock       sync.Mutex
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
			store:      opts.CompStore,
			recompiler: opts.Recompiler,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// recompile filters the given policies by app scope, compiles them and
// atomically swaps them on the gRPC API. Callers pass the prospective policy
// set and mutate the compstore only afterwards: metadata lists the compstore,
// and a policy listed there must already be enforced.
func (w *workflowAccessPolicies) recompile(all []wfaclapi.WorkflowAccessPolicy) {
	var scoped []wfaclapi.WorkflowAccessPolicy
	for _, p := range all {
		if p.IsAppScoped(w.appID) {
			scoped = append(scoped, p)
		}
	}
	compiled := workflowacl.Compile(scoped)
	w.recompiler(compiled)
	log.Infof("Recompiled %d workflow access policy resource(s) (of %d total)", len(scoped), len(all))
}

// The go inter does not yet understand that these functions are being used by
// the generic reconciler.
func (w *workflowAccessPolicies) update(ctx context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	if err := validate.WorkflowAccessPolicy(ctx, &policy); err != nil {
		log.Warnf("WorkflowAccessPolicy %q failed validation, skipping: %s", policy.Name, err)
		return
	}

	w.lock.Lock()
	defer w.lock.Unlock()

	all := w.store.ListWorkflowAccessPolicies()
	replaced := false
	for i, p := range all {
		if p.Name == policy.Name {
			all[i] = policy
			replaced = true
		}
	}
	if !replaced {
		all = append(all, policy)
	}
	w.recompile(all)
	w.store.AddWorkflowAccessPolicy(policy)
}

func (w *workflowAccessPolicies) delete(_ context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	w.lock.Lock()
	defer w.lock.Unlock()

	all := w.store.ListWorkflowAccessPolicies()
	remaining := make([]wfaclapi.WorkflowAccessPolicy, 0, len(all))
	for _, p := range all {
		if p.Name != policy.Name {
			remaining = append(remaining, p)
		}
	}
	w.recompile(remaining)
	w.store.DeleteWorkflowAccessPolicy(policy.Name)
}
