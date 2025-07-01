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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callCreateWorkflowStateMessage(ctx context.Context, events []*backend.OrchestrationRuntimeStateMessage) error {
	msgs := make([]proto.Message, len(events))
	targets := make([]string, len(events))

	for i, msg := range events {
		msgs[i] = &backend.CreateWorkflowInstanceRequest{StartEvent: msg.GetHistoryEvent()}
		targets[i] = msg.GetTargetInstanceID()
	}

	return o.callStateMessages(ctx, msgs, targets, todo.CreateWorkflowInstanceMethod)
}

func (o *orchestrator) callAddEventStateMessage(ctx context.Context, events []*backend.OrchestrationRuntimeStateMessage) error {
	targets := make([]string, len(events))
	msgs := make([]proto.Message, len(events))

	for i, msg := range events {
		msgs[i] = msg.GetHistoryEvent()
		targets[i] = msg.GetTargetInstanceID()
	}

	return o.callStateMessages(ctx, msgs, targets, todo.AddWorkflowEventMethod)
}

func (o *orchestrator) callStateMessages(ctx context.Context, msgs []proto.Message, targets []string, method string) error {
	errs := make([]error, len(msgs))

	var wg sync.WaitGroup
	wg.Add(len(msgs))
	for i, msg := range msgs {
		go func(i int, msg proto.Message, target string) {
			defer wg.Done()
			errs[i] = o.callStateMessage(ctx, msg, target, method)
		}(i, msg, targets[i])
	}

	wg.Wait()

	return errors.Join(errs...)
}

func (o *orchestrator) callStateMessage(ctx context.Context, m proto.Message, target string, method string) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", o.actorID, method, target)

	if _, err = o.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest(method).
		WithActor(o.actorType, target).
		WithData(b).
		WithContentType(invokev1.ProtobufContentType),
	); err != nil {
		return fmt.Errorf("failed to invoke method '%s' on actor '%s': %w", method, target, err)
	}

	return nil
}
