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

package state

import (
	contribstate "github.com/dapr/components-contrib/state"
	componentsstate "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/messages"
)

// strict stores enforce etags across the whole transaction.
type support struct {
	strict     bool
	createOnly bool
	reason     string
}

func supportOf(store contribstate.BaseStore) support {
	features := store.Features()
	_, withETags := store.(contribstate.TransactionalStoreWithETags)
	switch {
	case componentsstate.IsPluggable(store):
		return support{reason: "is a pluggable gRPC component, and pluggable state stores don't support transactional etags yet"}
	case !contribstate.FeatureTransactionalETags.IsPresent(features):
		return support{reason: "doesn't support the " + string(contribstate.FeatureTransactionalETags) + " feature"}
	case !withETags:
		return support{reason: "advertises " + string(contribstate.FeatureTransactionalETags) + " but doesn't implement MultiWithETags"}
	case !contribstate.FeatureCreateOnly.IsPresent(features):
		return support{strict: true, reason: "doesn't support the " + string(contribstate.FeatureCreateOnly) + " feature"}
	default:
		return support{strict: true, createOnly: true}
	}
}

func (s *state) check(sup support, storeName, actorType string, operations []contribstate.TransactionalStateOperation) error {
	for i, op := range operations {
		switch o := op.(type) {
		case contribstate.SetRequest:
			if o.Options.CreateOnly && !sup.createOnly {
				log.Debugf("Ignoring createOnly on actor state key '%s': actor state store '%s' %s", o.Key, storeName, sup.reason)
				o.Options.CreateOnly = false
				operations[i] = o
			}
			if o.HasETag() && !sup.strict {
				return s.rejectETag(storeName, actorType, sup.reason)
			}
		case contribstate.DeleteRequest:
			if o.HasETag() && !sup.strict {
				return s.rejectETag(storeName, actorType, sup.reason)
			}
		}
	}
	return nil
}

func (s *state) rejectETag(storeName, actorType, reason string) error {
	if _, logged := s.rejectedETagLogged.LoadOrStore(storeName+"/"+actorType, struct{}{}); !logged {
		log.Warnf("Rejected an actor state transaction with an etag for actor type '%s': actor state store '%s' %s. Logged once per actor type", actorType, storeName, reason)
	}
	return messages.ErrActorStateETagUnsupported.WithFormat(storeName, reason)
}

// LogSupport warns when the actor state store can't enforce etags or create-only.
func LogSupport(name string, store contribstate.Store) {
	features := store.Features()
	if !contribstate.FeatureETag.IsPresent(features) || !contribstate.FeatureTransactional.IsPresent(features) {
		return
	}

	switch sup := supportOf(store); {
	case !sup.strict:
		log.Warnf("Actor state etag protection is off: actor state store '%s' %s. Actor state reads return no etag, saves that send an etag are rejected, createOnly is ignored, and a stale save from an old actor activation can overwrite newer state", name, sup.reason)
	case !sup.createOnly:
		log.Warnf("Actor state create-only is off: actor state store '%s' %s. createOnly is ignored, so a stale create of a key can overwrite a newer one", name, sup.reason)
	}
}
