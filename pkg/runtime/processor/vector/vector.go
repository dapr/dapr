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

package vector

import (
	"context"
	"io"

	contribvector "github.com/dapr/components-contrib/vector"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compvector "github.com/dapr/dapr/pkg/components/vector"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	Registry *compvector.Registry
	Store    *compstore.ComponentStore
	Meta     *meta.Meta
}

type vector struct {
	registry *compvector.Registry
	store    *compstore.ComponentStore
	meta     *meta.Meta
}

func New(opts Options) *vector {
	return &vector{
		registry: opts.Registry,
		store:    opts.Store,
		meta:     opts.Meta,
	}
}

func (v *vector) Init(ctx context.Context, comp compapi.Component) error {
	// create the component
	fName := comp.LogName()

	vector, err := v.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if vector == nil {
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	// initialization
	meta, err := v.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	err = vector.Init(ctx, contribvector.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	v.store.AddVector(comp.Name, vector)

	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (v *vector) Close(comp compapi.Component) error {
	vector, ok := v.store.GetVector(comp.Name)
	if !ok {
		return nil
	}

	defer v.store.DeleteVector(comp.Name)

	closer, ok := vector.(io.Closer)
	if ok && closer != nil {
		err := closer.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
