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

package wfbackend

import (
	"context"
	"fmt"
	"sync"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	wfbeComp "github.com/dapr/dapr/pkg/components/wfbackend"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.workflowbackend")

type Options struct {
	Registry       *wfbeComp.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
}

type workflowBackend struct {
	registry             *wfbeComp.Registry
	compStore            *compstore.ComponentStore
	meta                 *meta.Meta
	lock                 sync.Mutex
	backendComponentInfo *wfbeComp.WorkflowBackendComponentInfo
}

func New(opts Options) *workflowBackend {
	return &workflowBackend{
		registry:  opts.Registry,
		compStore: opts.ComponentStore,
		meta:      opts.Meta,
	}
}

func (wfbe *workflowBackend) Init(ctx context.Context, comp compapi.Component) error {
	wfbe.lock.Lock()
	defer wfbe.lock.Unlock()

	// create the component
	fName := comp.LogName()
	workflowBackendComp, err := wfbe.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		log.Warnf("error creating workflow backend component (%s): %s", comp.LogName(), err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		wfbe.backendComponentInfo = &wfbeComp.WorkflowBackendComponentInfo{
			InvalidWorkflowBackend: true,
		}
		return err
	}

	if workflowBackendComp == nil {
		return nil
	}

	// initialization
	baseMetadata, err := wfbe.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}
	err = workflowBackendComp.Init(wfbeComp.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	// save workflow related configuration
	wfbe.compStore.AddWorkflowBackend(comp.ObjectMeta.Name, workflowBackendComp)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	if wfbe.backendComponentInfo == nil {
		log.Info("Using '" + comp.Spec.Type + "' as workflow backend")
		wfbe.backendComponentInfo = &wfbeComp.WorkflowBackendComponentInfo{
			WorkflowBackendType:     comp.Spec.Type,
			WorkflowBackendMetadata: baseMetadata,
		}
	} else if wfbe.backendComponentInfo.WorkflowBackendType != comp.Spec.Type {
		return fmt.Errorf("detected duplicate workflow backend: %s and %s", wfbe.backendComponentInfo.WorkflowBackendType, comp.Spec.Type)
	}

	return nil
}

func (wfbe *workflowBackend) Close(comp compapi.Component) error {
	wfbe.lock.Lock()
	defer wfbe.lock.Unlock()

	// We don't "Close" a workflow here because that has no meaning today since
	// Dapr doesn't support third-party workflows. Internal workflows are based
	// on the actor subsystem so there is nothing to close.
	wfbe.compStore.DeleteWorkflowBackend(comp.Name)

	return nil
}

func (wfbe *workflowBackend) WorkflowBackendComponentInfo() (*wfbeComp.WorkflowBackendComponentInfo, bool) {
	wfbe.lock.Lock()
	defer wfbe.lock.Unlock()

	if wfbe.backendComponentInfo == nil {
		return nil, false
	}
	return wfbe.backendComponentInfo, true
}
