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

package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"google.golang.org/protobuf/proto"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func (w *workflow) callActivities(ctx context.Context, es []*backend.HistoryEvent, generation uint64) error {
	var wg sync.WaitGroup
	var errs []error
	var lock sync.Mutex

	wg.Add(len(es))
	for _, e := range es {
		go func(e *backend.HistoryEvent) {
			defer wg.Done()

			err := w.callActivity(ctx, e, generation)
			if errors.Is(err, todo.ErrDuplicateInvocation) {
				log.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", w.actorID, e.GetTaskScheduled().GetName(), e.GetEventId())
				return
			}

			if err != nil {
				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
		}(e)
	}

	wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (w *workflow) callActivity(ctx context.Context, e *backend.HistoryEvent, generation uint64) error {
	ts := e.GetTaskScheduled()
	if ts == nil {
		log.Warnf("Workflow actor '%s': unable to process task '%v'", w.actorID, e)
		return nil
	}

	var eventData []byte
	eventData, err := proto.Marshal(e)
	if err != nil {
		return err
	}

	targetActorID := buildActivityActorID(w.actorID, e.GetEventId(), generation)

	w.activityResultAwaited.Store(true)

	log.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s'", w.actorID, targetActorID)

	_, err = w.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest("Execute").
		WithActor(w.activityActorType, targetActorID).
		WithData(eventData).
		WithContentType(invokev1.ProtobufContentType),
	)
	if err != nil {
		return fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err)
	}

	return nil
}

func buildActivityActorID(workflowID string, taskID int32, generation uint64) string {
	// An activity can be identified by its name followed by its task ID and generation. Example: SayHello::0::1, SayHello::1::1, etc.
	return workflowID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}
