/*
Copyright 2025 The Dapr Authors
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

package binarystore

import (
	"context"
	"io"

	contribbinarystore "github.com/dapr/components-contrib/binarystore"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compbinarystore "github.com/dapr/dapr/pkg/components/binarystore"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	Registry *compbinarystore.Registry
	Store    *compstore.ComponentStore
	Meta     *meta.Meta
}

type binarystoremgr struct {
	registry *compbinarystore.Registry
	store    *compstore.ComponentStore
	meta     *meta.Meta
}

func New(opts Options) *binarystoremgr {
	return &binarystoremgr{
		registry: opts.Registry,
		store:    opts.Store,
		meta:     opts.Meta,
	}
}

func (b *binarystoremgr) Init(ctx context.Context, comp compapi.Component) error {
	fName := comp.LogName()

	store, err := b.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if store == nil {
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	meta, err := b.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	err = store.Init(ctx, contribbinarystore.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	b.store.AddBinaryStore(comp.Name, store)

	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (b *binarystoremgr) Close(comp compapi.Component) error {
	store, ok := b.store.GetBinaryStore(comp.Name)
	if !ok {
		return nil
	}

	defer b.store.DeleteBinaryStore(comp.Name)

	closer, ok := store.(io.Closer)
	if ok && closer != nil {
		err := closer.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
