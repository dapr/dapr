/*
Copyright 2023 The Dapr Authors
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

package processor

import (
	"context"

	"github.com/dapr/components-contrib/workflows"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compworkflow "github.com/dapr/dapr/pkg/components/workflows"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type workflow struct {
	registry  *compworkflow.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
}

func (w *workflow) init(ctx context.Context, comp compapi.Component) error {
	// create the component
	fName := comp.LogName()
	workflowComp, err := w.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		log.Warnf("error creating workflow component (%s): %s", comp.LogName(), err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return err
	}

	if workflowComp == nil {
		return nil
	}

	// initialization
	baseMetadata := w.meta.ToBaseMetadata(comp)
	err = workflowComp.Init(workflows.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	// save workflow related configuration
	w.compStore.AddWorkflow(comp.ObjectMeta.Name, workflowComp)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}
