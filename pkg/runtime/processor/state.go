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
	"fmt"
	"strings"
	"sync"

	contribstate "github.com/dapr/components-contrib/state"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compstate "github.com/dapr/dapr/pkg/components/state"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/utils"
)

const (
	propertyKeyActorStateStore = "actorstatestore"
)

type state struct {
	registry  *compstate.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
	lock      sync.Mutex

	actorStateStoreName string
	placementEnabled    bool
}

func (s *state) init(ctx context.Context, comp compapi.Component) error {
	fName := comp.LogName()
	store, err := s.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if store != nil {
		secretStoreName := s.meta.AuthSecretStoreOrDefault(&comp)

		secretStore, _ := s.compStore.GetSecretStore(secretStoreName)
		encKeys, encErr := encryption.ComponentEncryptionKey(comp, secretStore)
		if encErr != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
		}

		if encKeys.Primary.Key != "" {
			ok := encryption.AddEncryptedStateStore(comp.ObjectMeta.Name, encKeys)
			if ok {
				log.Infof("automatic encryption enabled for state store %s", comp.ObjectMeta.Name)
			}
		}

		baseMetadata := s.meta.ToBaseMetadata(comp)
		props := baseMetadata.Properties
		err = store.Init(ctx, contribstate.Metadata{Base: baseMetadata})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}

		s.compStore.AddStateStore(comp.ObjectMeta.Name, store)
		err = compstate.SaveStateConfiguration(comp.ObjectMeta.Name, props)
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			wrapError := fmt.Errorf("failed to save lock keyprefix: %s", err.Error())
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, wrapError)
		}

		// when placement address list is not empty, set specified actor store.
		if s.placementEnabled {
			// set specified actor store if "actorStateStore" is true in the spec.
			actorStoreSpecified := false
			for k, v := range props {
				//nolint:gocritic
				if strings.ToLower(k) == propertyKeyActorStateStore {
					actorStoreSpecified = utils.IsTruthy(v)
					break
				}
			}

			if actorStoreSpecified {
				s.lock.Lock()
				defer s.lock.Unlock()
				if s.actorStateStoreName == "" {
					log.Info("Using '" + comp.ObjectMeta.Name + "' as actor state store")
					s.actorStateStoreName = comp.ObjectMeta.Name
				} else if s.actorStateStoreName != comp.ObjectMeta.Name {
					// TODO: @joshvanl: Don't fatal here. Return error.
					log.Fatalf("Detected duplicate actor state store: %s and %s", s.actorStateStoreName, comp.ObjectMeta.Name)
				}
			}
		}

		diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)
	}

	return nil
}
