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

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

type subscriptions struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	loader.Loader[subapi.Subscription]
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (s *subscriptions) update(ctx context.Context, sub subapi.Subscription) {
	oldSub, exists := s.store.GetDeclarativeSubscription(sub.Name)

	if exists {
		log.Infof("Closing existing Subscription to reload: %s", oldSub.Name)
		if err := s.proc.CloseSubscription(ctx, oldSub); err != nil {
			log.Errorf("Failed to close existing Subscription: %s", err)
			return
		}
	}

	log.Infof("Adding Subscription for processing: %s", sub.Name)
	if s.proc.AddPendingSubscription(ctx, sub) {
		log.Infof("Subscription updated: %s", sub.Name)
	}

	return
}

//nolint:unused
func (s *subscriptions) delete(ctx context.Context, sub subapi.Subscription) {
	if err := s.proc.CloseSubscription(ctx, sub); err != nil {
		log.Errorf("Failed to close Subscription: %s", err)
	}
}
