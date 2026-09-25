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

package claim

import (
	"context"
	"encoding/json"
	"time"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
)

func (g *Guards) write(ctx context.Context, actorID, taskKey string, completed bool) {
	octx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := g.opts.State.TransactionalStateOperation(octx, true, &actorsapi.TransactionalRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Operations: []actorsapi.TransactionalOperation{{
			Operation: actorsapi.Upsert,
			Request: actorsapi.TransactionalUpsert{
				Key: recordStateKey,
				Value: Record{
					TaskKey:     taskKey,
					HeartbeatMs: time.Now().UnixMilli(),
					Completed:   completed,
				},
			},
		}},
	}, false)
	if err != nil {
		log.Warnf("Activity actor '%s': failed to write the execution-claim record; recovery degrades to at-least-once for this handoff: %v", actorID, err)
	}
}

// delete removes the record, conditional on etag when the read that observed
// it returned one, so a concurrent overwrite is kept rather than destroyed.
func (g *Guards) delete(ctx context.Context, actorID string, etag *string) error {
	octx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := g.opts.State.TransactionalStateOperation(octx, true, &actorsapi.TransactionalRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Operations: []actorsapi.TransactionalOperation{{
			Operation: actorsapi.Delete,
			Request:   actorsapi.TransactionalDelete{Key: recordStateKey, ETag: etag},
		}},
	}, false)
	if err != nil {
		log.Warnf("Activity actor '%s': failed to delete the execution-claim record: %v", actorID, err)
	}
	return err
}

func (g *Guards) read(ctx context.Context, actorID string) (*Record, *string, error) {
	octx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	res, err := g.opts.State.Get(octx, &actorsapi.GetStateRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Key:       recordStateKey,
	}, false)
	if err != nil {
		return nil, nil, err
	}
	if res == nil || len(res.Data) == 0 {
		return nil, nil, nil
	}
	var rec Record
	if err := json.Unmarshal(res.Data, &rec); err != nil {
		return nil, nil, err
	}
	return &rec, res.ETag, nil
}
