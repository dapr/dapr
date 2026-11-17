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

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/kit/events/loop"
)

const queueKey = "queue"

// call is an Enqueue or Done for a key, answered on resp.
type call struct {
	ctx    context.Context
	method string
	data   []byte
	resp   chan result
}

type result struct {
	data []byte
	err  error
}

// key runs the calls of one ordering key on its loop.
type key struct {
	*factory
	id      string
	loop    loop.Interface[any]
	pending int
}

func (k *key) Handle(_ context.Context, ev any) error {
	c, ok := ev.(*call)
	if !ok {
		return nil
	}
	// A call that waited past its caller's timeout still lands, on its own
	// clock: the caller's retry or redrive expects its effect.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), callTimeout)
	defer cancel()
	var r result
	switch c.method {
	case EnqueueMethod:
		var e Entry
		if r.err = json.Unmarshal(c.data, &e); r.err == nil {
			r.data, r.err = k.enqueue(ctx, e)
		}
	case DoneMethod:
		var d DoneRequest
		if r.err = json.Unmarshal(c.data, &d); r.err == nil {
			r.err = k.update(ctx, func(q *queue) (bool, *head) { return q.done(d.Entry, d.Outcome) })
		}
	default:
		r.err = fmt.Errorf("no such method: %s", c.method)
	}
	c.resp <- r
	k.finish()
	return nil
}

func (k *key) enqueue(ctx context.Context, e Entry) ([]byte, error) {
	var existing bool
	err := k.update(ctx, func(q *queue) (bool, *head) {
		var h *head
		existing, h = q.enqueue(e)
		return !existing, h
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(existing)
}

// update applies fn to the stored queue, saves it when fn changed it, and
// notifies the head fn returns. Saving first means no reminder is created
// for a change that is not stored; a failed notify is repaired by the
// caller's retry, which finds nothing to change and notifies the head again.
// The row's ETag guards a save against a peer host still serving the key
// after placement moved it: a mismatch is an error the caller retries.
func (k *key) update(ctx context.Context, fn func(*queue) (bool, *head)) error {
	q, etag, err := k.load(ctx)
	if err != nil {
		return err
	}
	changed, h := fn(&q)
	if changed {
		if err = k.save(ctx, &q, etag); err != nil {
			return err
		}
	}
	return k.notify(ctx, h)
}

func (k *key) load(ctx context.Context) (queue, *string, error) {
	var q queue
	res, err := k.state.Get(ctx, &actorapi.GetStateRequest{ActorType: k.actorType, ActorID: k.id, Key: queueKey}, false)
	if err != nil {
		return q, nil, fmt.Errorf("failed to load ordering key '%s': %w", k.id, err)
	}
	if len(res.Data) > 0 {
		if err = json.Unmarshal(res.Data, &q); err != nil {
			return q, nil, fmt.Errorf("invalid ordering key '%s': %w", k.id, err)
		}
	}
	return q, res.ETag, nil
}

func (k *key) save(ctx context.Context, q *queue, etag *string) error {
	op := actorapi.TransactionalOperation{
		Operation: actorapi.Delete,
		Request:   actorapi.TransactionalDelete{Key: queueKey, ETag: etag},
	}
	if !q.empty() {
		b, err := json.Marshal(q)
		if err != nil {
			return err
		}
		op = actorapi.TransactionalOperation{
			Operation: actorapi.Upsert,
			Request:   actorapi.TransactionalUpsert{Key: queueKey, Value: b, ETag: etag},
		}
	}
	return k.state.TransactionalStateOperation(ctx, true, &actorapi.TransactionalRequest{
		ActorType:  k.actorType,
		ActorID:    k.id,
		Operations: []actorapi.TransactionalOperation{op},
	}, false)
}

// notify creates the reminder that releases or cancels h. A release is due
// at its scheduled start and counts towards its workflow's concurrency
// limits; a cancel runs no workflow code, so it is due now and counts towards
// none. A repeat overwrites the reminder by name.
func (k *key) notify(ctx context.Context, h *head) error {
	if h == nil {
		return nil
	}
	rd := ReminderData{Key: k.id}
	if h.After != nil {
		rd.After = h.After.InstanceID
	}
	b, err := json.Marshal(rd)
	if err != nil {
		return err
	}
	req := &actorapi.CreateReminderRequest{
		ActorType:     k.wfActorType,
		ActorID:       h.InstanceID,
		Name:          CancelReminderName(h.CreateExecutionID),
		Data:          &anypb.Any{Value: b},
		DueTime:       time.Now().UTC().Format(time.RFC3339Nano),
		FailurePolicy: common.RetryForeverPolicy(),
	}
	if !h.Cancel {
		req.Name = ReleaseReminderName(h.CreateExecutionID)
		if h.DueTime != nil && h.DueTime.After(time.Now()) {
			req.DueTime = h.DueTime.UTC().Format(time.RFC3339Nano)
		}
		if h.WorkflowName != "" {
			req.ConcurrencyKey = &h.WorkflowName
		}
	}
	if err = k.reminders.Create(ctx, req); err != nil {
		return fmt.Errorf("failed to create reminder '%s' of workflow '%s' for ordering key '%s': %w", req.Name, h.InstanceID, k.id, err)
	}
	return nil
}

// finish records that a call completed, and stops the loop once the key has
// no calls left. A loop is only stopped under the lock that dispatch enqueues
// under, so no call is ever enqueued to a stopping loop.
func (k *key) finish() {
	k.lock.Lock()
	defer k.lock.Unlock()
	k.pending--
	if k.pending == 0 && k.keys[k.id] == k {
		delete(k.keys, k.id)
		go k.loop.Close(nil)
	}
}
