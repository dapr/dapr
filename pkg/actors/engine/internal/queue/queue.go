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

package queue

import (
	"context"
	"errors"
	"sync"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine/internal/api"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Queue struct {
	engine *api.API

	lock    sync.Mutex
	closeCh chan struct{}

	queues  map[string]*inflight
	queueCh chan *qreq
	wg      sync.WaitGroup
}

type qreq struct {
	key    string
	goCh   chan struct{}
	doneCh chan struct{}
}

type inflight struct {
	ch      chan struct{}
	pending uint64
}

func New(engine *api.API) *Queue {
	return &Queue{
		engine:  engine,
		queueCh: make(chan *qreq),
		closeCh: make(chan struct{}),
		queues:  make(map[string]*inflight),
	}
}

func (q *Queue) Run(ctx context.Context) error {
	defer q.wg.Wait()
	defer close(q.closeCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req := <-q.queueCh:

			q.lock.Lock()
			fl, ok := q.queues[req.key]
			if !ok {
				fl = &inflight{
					pending: 1,
					ch:      make(chan struct{}, 1),
				}
				fl.ch <- struct{}{}
				close(req.goCh)

				q.wg.Add(1)
				go func() {
					defer q.wg.Done()

					<-req.doneCh
					q.lock.Lock()
					defer q.lock.Unlock()

					fl.pending--
					if fl.pending == 0 {
						delete(q.queues, req.key)
						return
					}

					<-fl.ch
				}()

				q.lock.Unlock()
				continue
			}

			fl.pending++

			q.wg.Add(1)
			go func() {
				defer q.wg.Done()
				fl.ch <- struct{}{}
				close(req.goCh)
				<-req.doneCh

				q.lock.Lock()
				defer q.lock.Unlock()

				fl.pending--
				if fl.pending == 0 {
					delete(q.queues, req.key)
					return
				}

				<-fl.ch
			}()

			q.lock.Unlock()
		}
	}
}

func (q *Queue) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	qreq := &qreq{
		goCh:   make(chan struct{}),
		doneCh: make(chan struct{}),
		key:    req.GetActor().GetActorType() + "||" + req.GetActor().GetActorId(),
	}

	select {
	case q.queueCh <- qreq:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-q.closeCh:
		return nil, errors.New("queue closed")
	}

	defer close(qreq.doneCh)

	select {
	case <-qreq.goCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-q.closeCh:
		return nil, errors.New("queue closed")
	}

	return q.engine.Call(ctx, req)
}

func (q *Queue) CallReminder(ctx context.Context, reminder *actorsapi.Reminder) error {
	qreq := &qreq{
		goCh:   make(chan struct{}),
		doneCh: make(chan struct{}),
		key:    reminder.ActorType + "||" + reminder.ActorID,
	}

	select {
	case q.queueCh <- qreq:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closeCh:
		return errors.New("queue closed")
	}

	defer close(qreq.doneCh)

	select {
	case <-qreq.goCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closeCh:
		return errors.New("queue closed")
	}

	return q.engine.CallReminder(ctx, reminder)
}

func (q *Queue) CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error {
	qreq := &qreq{
		goCh:   make(chan struct{}),
		doneCh: make(chan struct{}),
		key:    req.GetActor().GetActorType() + "||" + req.GetActor().GetActorId(),
	}

	select {
	case q.queueCh <- qreq:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closeCh:
		return errors.New("queue closed")
	}

	defer close(qreq.doneCh)

	select {
	case <-qreq.goCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closeCh:
		return errors.New("queue closed")
	}

	return q.engine.CallStream(ctx, req, stream)
}
