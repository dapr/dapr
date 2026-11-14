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

// Package ordering runs the queues of workflow ordering keys. Each key is an
// actor with one row listing the instances created with the key, in the order
// they were accepted. A reminder on its workflow actor releases the head, and
// the next instance is released once the head reports that it completed. The
// actor never calls a workflow actor, so one can call it holding its own lock.
//
// The row's ETag guards every save but the first: the actor state API has no
// insert-if-absent, so two hosts that both serve an idle key during a
// placement move can each write the first row and release their own head,
// and the order between those two starts is not kept. The row records the
// last writer's queue, every later save is guarded, and the other head's Done
// finds nothing to change.
package ordering

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// ordering is the actor handle of a key: it hands each call to the key's loop.
type ordering struct {
	*factory
	actorID string
}

func (o *ordering) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	c := &call{ctx: ctx, method: req.GetMessage().GetMethod(), data: req.GetMessage().GetData().GetValue(), resp: make(chan result, 1)}
	o.dispatch(o.actorID, c)
	var r result
	select {
	case r = <-c.resp:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	return &internalsv1pb.InternalInvokeResponse{
		Status:  &internalsv1pb.Status{Code: http.StatusOK},
		Message: &commonv1pb.InvokeResponse{Data: &anypb.Any{Value: r.data}},
	}, nil
}

func (o *ordering) InvokeReminder(context.Context, *actorapi.Reminder) error {
	return errors.New("reminders are not implemented")
}

func (o *ordering) InvokeTimer(context.Context, *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (o *ordering) InvokeStream(context.Context, *internalsv1pb.InternalInvokeRequest, func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	return errors.New("invoke stream is not implemented")
}

func (o *ordering) Deactivate(context.Context) error { return nil }

func (o *ordering) Key() string { return o.actorType + actorapi.DaprSeparator + o.actorID }

func (o *ordering) Type() string { return o.actorType }

func (o *ordering) ID() string { return o.actorID }
