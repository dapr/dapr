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
	"errors"
	"sync"
	"time"

	"github.com/microsoft/durabletask-go/backend"

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
	AppID          string
	Registry       *wfbeComp.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
}

type workflowBackend struct {
	registry  *wfbeComp.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
	lock      sync.Mutex
	backend   backend.Backend
	appID     string
}

func New(opts Options) *workflowBackend {
	return &workflowBackend{
		registry:  opts.Registry,
		compStore: opts.ComponentStore,
		meta:      opts.Meta,
		appID:     opts.AppID,
	}
}

func (w *workflowBackend) Init(ctx context.Context, comp compapi.Component) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.backend != nil {
		// Can only have 1 workflow backend component
		return errors.New("cannot create more than one workflow backend component")
	}

	// Create the component
	fName := comp.LogName()
	beFactory, err := w.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		log.Errorf("Error creating workflow backend component (%s): %v", fName, err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return err
	}

	if beFactory == nil {
		return nil
	}

	// Initialization
	baseMetadata, err := w.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	be, err := beFactory(wfbeComp.Metadata{
		AppID: w.appID,
		Base:  baseMetadata,
	})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	log.Infof("Using %s as workflow backend", comp.Spec.Type)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)
	w.backend = be
	w.compStore.AddWorkflowBackend(comp.Name, be)

	return nil
}

func (w *workflowBackend) Close(comp compapi.Component) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	backend, ok := w.compStore.GetWorkflowBackend(comp.Name)
	if !ok {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	defer w.compStore.DeleteWorkflowBackend(comp.Name)
	w.backend = nil

	return backend.Stop(ctx)
}

func (w *workflowBackend) Backend() (backend.Backend, bool) {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.backend == nil {
		return nil, false
	}
	return w.backend, true
}
