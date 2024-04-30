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

package informer

import (
	"context"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/dapr/utils"
)

type handler[T meta.Resource] struct {
	i       *informer[T]
	batchCh <-chan *informerEvent[T]
	appCh   chan<- *Event[T]
	id      *spiffe.Parsed
}

func (h *handler[T]) loop(ctx context.Context) {
	defer close(h.appCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.i.closeCh:
			return
		case event := <-h.batchCh:
			env, ok := h.appEventFromEvent(event)
			if !ok {
				continue
			}

			select {
			case <-ctx.Done():
			case <-h.i.closeCh:
			case h.appCh <- env:
			}
		}
	}
}

func (h *handler[T]) appEventFromEvent(event *informerEvent[T]) (*Event[T], bool) {
	if event.newObj.Manifest.GetNamespace() != h.id.Namespace() {
		return nil, false
	}

	// Handle case where scope is removed from manifest through update.
	var appInOld bool
	if event.oldObj != nil {
		manifest := *event.oldObj
		appInOld = len(manifest.GetScopes()) == 0 || utils.Contains(manifest.GetScopes(), h.id.AppID())
	}

	newManifest := event.newObj.Manifest
	var appInNew bool
	if len(newManifest.GetScopes()) == 0 || utils.Contains(newManifest.GetScopes(), h.id.AppID()) {
		appInNew = true
	}

	if !appInNew {
		if !appInOld {
			return nil, false
		}

		return &Event[T]{
			Manifest: *event.oldObj,
			Type:     operatorv1.ResourceEventType_DELETED,
		}, true
	}

	env := event.newObj
	if !appInOld && env.Type == operatorv1.ResourceEventType_UPDATED {
		env.Type = operatorv1.ResourceEventType_CREATED
	}

	return env, true
}
