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

package ordering

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/actors/router"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// callTimeout bounds a call to a key, which a workflow actor makes holding
// its own lock.
const callTimeout = 5 * time.Second

// Call invokes method on the key of actorType with payload: an Entry for
// Enqueue, a DoneRequest for Done. It outlives a cancelled
// context, since its effect must match state the caller already saved.
func Call(ctx context.Context, r router.Interface, actorType, method, key string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	defer cancel()
	res, err := r.Call(ctx, internalsv1pb.NewInternalInvokeRequest(method).
		WithActor(actorType, key).
		WithData(data).
		WithContentType(invokev1.JSONContentType))
	if err != nil {
		return nil, fmt.Errorf("ordering key '%s' %s: %w", key, method, err)
	}
	return res.GetMessage().GetData().GetValue(), nil
}
