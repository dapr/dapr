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

	"github.com/dapr/components-contrib/secretstores"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compsecret "github.com/dapr/dapr/pkg/components/secretstores"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type secret struct {
	registry  *compsecret.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
}

func (s *secret) init(ctx context.Context, comp compapi.Component) error {
	fName := comp.LogName()
	secretStore, err := s.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	err = secretStore.Init(ctx, secretstores.Metadata{Base: s.meta.ToBaseMetadata(comp)})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	s.compStore.AddSecretStore(comp.ObjectMeta.Name, secretStore)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}
