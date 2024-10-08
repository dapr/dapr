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

package middleware

import (
	"context"
	"fmt"

	contribmiddle "github.com/dapr/components-contrib/middleware"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compmiddlehttp "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/middleware/http"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	// Metadata is the metadata helper.
	Meta *meta.Meta

	// RegistryHTTP is the HTTP middleware registry.
	RegistryHTTP *compmiddlehttp.Registry

	// HTTP is the HTTP middleware pipeline.
	HTTP *http.HTTP
}

// middleware is a component that implements the middleware interface.
type middleware struct {
	meta         *meta.Meta
	registryHTTP *compmiddlehttp.Registry
	http         *http.HTTP
}

func New(opts Options) *middleware {
	return &middleware{
		meta:         opts.Meta,
		registryHTTP: opts.RegistryHTTP,
		http:         opts.HTTP,
	}
}

func (m *middleware) Init(_ context.Context, comp compapi.Component) error {
	meta, err := m.meta.ToBaseMetadata(comp)
	if err != nil {
		return err
	}

	middle, err := m.registryHTTP.Create(comp.Spec.Type, comp.Spec.Version, contribmiddle.Metadata{Base: meta}, comp.LogName())
	if err != nil {
		return rterrors.NewInit(rterrors.CreateComponentFailure, comp.LogName(),
			fmt.Errorf("process component %s error: %w", comp.Name, err),
		)
	}

	m.http.Add(http.Spec{
		Component:      comp,
		Implementation: middle,
	})

	return nil
}

func (m *middleware) Close(comp compapi.Component) error {
	m.http.Remove(comp.Name)
	return nil
}
