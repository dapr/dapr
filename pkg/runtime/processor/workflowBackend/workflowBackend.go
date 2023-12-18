/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwwbe.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflowBackend

import (
	"context"
	"fmt"
	"sync"

	wfbe "github.com/dapr/components-contrib/wfbackend"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	workflowBackendComp "github.com/dapr/dapr/pkg/components/workflowBackend"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.workflowbackend")

type Options struct {
	Registry       *workflowBackendComp.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
}

type workflowBackend struct {
	registry             *workflowBackendComp.Registry
	compStore            *compstore.ComponentStore
	meta                 *meta.Meta
	lock                 sync.Mutex
	backendComponentInfo *wfbe.WorkflowBackendComponentInfo
}

func New(opts Options) *workflowBackend {
	return &workflowBackend{
		registry:  opts.Registry,
		compStore: opts.ComponentStore,
		meta:      opts.Meta,
	}
}

func (wbe *workflowBackend) Init(ctx context.Context, comp compapi.Component) error {
	wbe.lock.Lock()
	defer wbe.lock.Unlock()

	// create the component
	fName := comp.LogName()
	workflowBackendComp, err := wbe.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		log.Warnf("error creating workflow backend component (%s): %s", comp.LogName(), err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		wbe.backendComponentInfo = &wfbe.WorkflowBackendComponentInfo{
			InvalidWorkflowBackend: true,
		}
		return err
	}

	if workflowBackendComp == nil {
		return nil
	}

	// initialization
	baseMetadata, err := wbe.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}
	err = workflowBackendComp.Init(wfbe.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	// save workflow related configuration
	wbe.compStore.AddWorkflowBackend(comp.ObjectMeta.Name, workflowBackendComp)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	if wbe.backendComponentInfo == nil {
		log.Info("Using '" + comp.Spec.Type + "' as workflow backend")
		wbe.backendComponentInfo = &wfbe.WorkflowBackendComponentInfo{
			WorkflowBackendType:     comp.Spec.Type,
			WorkflowBackendMetadata: baseMetadata,
		}
	} else if wbe.backendComponentInfo.WorkflowBackendType != comp.Spec.Type {
		return fmt.Errorf("detected duplicate workflow backend: %s and %s", wbe.backendComponentInfo.WorkflowBackendType, comp.Spec.Type)
	}

	return nil
}

func (wbe *workflowBackend) Close(comp compapi.Component) error {
	wbe.lock.Lock()
	defer wbe.lock.Unlock()

	// We don't "Close" a workflow here because that has no meaning today since
	// Dapr doesn't support third-party workflows. Internal workflows are based
	// on the actor subsystem so there is nothing to close.
	wbe.compStore.DeleteWorkflowBackend(comp.Name)

	return nil
}

func (wbe *workflowBackend) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	wbe.lock.Lock()
	defer wbe.lock.Unlock()

	if wbe.backendComponentInfo == nil {
		return nil, false
	}
	return wbe.backendComponentInfo, true
}
