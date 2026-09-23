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

package search

import (
	"context"
	"io"

	contribsearch "github.com/dapr/components-contrib/search"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compsearch "github.com/dapr/dapr/pkg/components/search"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	Registry *compsearch.Registry
	Store    *compstore.ComponentStore
	Meta     *meta.Meta
}

type search struct {
	registry *compsearch.Registry
	store    *compstore.ComponentStore
	meta     *meta.Meta
}

func New(opts Options) *search {
	return &search{
		registry: opts.Registry,
		store:    opts.Store,
		meta:     opts.Meta,
	}
}

func (s *search) Init(ctx context.Context, comp compapi.Component) error {
	// create the component
	fName := comp.LogName()

	search, err := s.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if search == nil {
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	// initialization
	meta, err := s.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	err = search.Init(ctx, contribsearch.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	s.store.AddSearch(comp.Name, search)

	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (s *search) Close(comp compapi.Component) error {
	search, ok := s.store.GetSearch(comp.Name)
	if !ok {
		return nil
	}

	defer s.store.DeleteSearch(comp.Name)

	closer, ok := search.(io.Closer)
	if ok && closer != nil {
		err := closer.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
