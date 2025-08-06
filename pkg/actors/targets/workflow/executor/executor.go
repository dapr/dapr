/*
Copyright 2025 The Dapr Authors
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

package executor

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/lock"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	MethodComplete      = "Complete"
	MethodCancel        = "Cancel"
	MethodWatchComplete = "WatchComplete"
)

type executor struct {
	*factory
	actorID string
	lock    *lock.Lock

	closeCh    chan struct{}
	completeCh chan *internalsv1pb.InternalInvokeResponse
	cancelCh   chan struct{}

	watchLock chan struct{}
}

func (e *executor) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	switch req.GetMessage().GetMethod() {
	case MethodComplete:
		return nil, e.complete(ctx, req)
	case MethodCancel:
		return nil, e.cancel()
	default:
		return nil, errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) complete(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) error {
	d := &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{
			Code: int32(codes.OK),
		},
		Message: &commonv1pb.InvokeResponse{
			Data: req.GetMessage().GetData(),
		},
	}

	select {
	case e.completeCh <- d:
		return nil
	case <-e.cancelCh:
		return errors.New("canceled before completion result was sent")
	case <-e.closeCh:
		return errors.New("executor closed")
	case <-ctx.Done():
		return errors.New("context cancelled before completion result was sent")
	}
}

func (e *executor) cancel() error {
	close(e.cancelCh)
	return nil
}

func (e *executor) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("reminders are not implemented")
}

func (e *executor) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (e *executor) Deactivate(_ context.Context) error {
	close(e.closeCh)
	e.table.Delete(e.actorID)
	executorCache.Put(e)
	return nil
}

func (e *executor) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, ch chan<- *internalsv1pb.InternalInvokeResponse) error {
	switch req.GetMessage().GetMethod() {
	case MethodWatchComplete:
		return e.watchComplete(ctx, ch)
	default:
		return errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) watchComplete(ctx context.Context, ch chan<- *internalsv1pb.InternalInvokeResponse) error {
	defer e.Deactivate(ctx)

	select {
	case e.watchLock <- struct{}{}:
	case <-e.closeCh:
		return errors.New("executor closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		<-e.watchLock
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.closeCh:
		return errors.New("executor closed")
	case <-e.cancelCh:
		ch <- &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
				Code: int32(codes.Aborted),
			},
		}
		return nil
	case d := <-e.completeCh:
		ch <- d
		return nil
	}
}

func (e *executor) Key() string {
	return e.actorType + actorapi.DaprSeparator + e.actorID
}

func (e *executor) Type() string {
	return e.actorType
}

func (e *executor) ID() string {
	return e.actorID
}
