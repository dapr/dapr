/*
Copyright 2024 The Dapr Authors
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

package reconciler

import (
	"context"
	"time"

	"k8s.io/utils/clock"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

type Secrets struct {
	compStore     *compstore.ComponentStore
	secretManager processor.SecretManager
	manager       manager[compapi.Component]

	clock clock.WithTicker
}

func NewSecrets(opts Options[compapi.Component], manager manager[compapi.Component]) *Secrets {
	var secMngr processor.SecretManager
	if opts.Processor != nil {
		secMngr = opts.Processor.Secret()
	}

	return &Secrets{
		compStore:     opts.CompStore,
		secretManager: secMngr,
		manager:       manager,
		clock:         clock.RealClock{},
	}
}

func (s *Secrets) Run(ctx context.Context) error {
	if s.secretManager == nil {
		<-ctx.Done()
		return nil
	}

	// Periodic secrets polling.
	// Default to 1 minute, but maybe should be configurable.
	ticker := s.clock.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			s.reconcile(ctx)
		}
	}
}

func (s *Secrets) reconcile(ctx context.Context) {
	log.Debug("Starting secrets hot reload reconciliation")
	comps := s.compStore.ListComponents()

	for _, oldComp := range comps {
		newComp := *oldComp.DeepCopy()
		updated, _ := s.secretManager.ProcessResource(ctx, &newComp)
		if !updated {
			continue
		}

		if !differ.AreSame(oldComp, newComp) {
			log.Infof("Secrets updated for component %s, triggers hot reload", oldComp.LogName())
			s.manager.update(ctx, newComp)
		}
	}
}
