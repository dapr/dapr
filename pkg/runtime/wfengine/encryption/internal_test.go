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

package encryption

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/logger"
)

func TestSelfCheckWaitsForComponent(t *testing.T) {
	t.Parallel()
	e, err := New(Options{
		ComponentStore: compstore.New(),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		ComponentName:  "missing",
		KeyName:        "kek",
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	require.ErrorContains(t, e.SelfCheck(ctx), "crypto component 'missing' not found")
}

// TestDescriptorCoverage classifies every string, bytes and
// google.protobuf.StringValue field reachable from a persisted history event
// or propagated history. It fails when durabletask adds such a field, so that
// whoever bumps it decides whether the new field is a payload.
func TestDescriptorCoverage(t *testing.T) {
	t.Parallel()

	sealed := []string{
		"ChildWorkflowInstanceCompletedEvent.result",
		"ChildWorkflowInstanceCreatedEvent.input",
		"ContinueAsNewEvent.input",
		"EventRaisedEvent.input",
		"EventSentEvent.input",
		"ExecutionCompletedEvent.result",
		"ExecutionResumedEvent.input",
		"ExecutionStartedEvent.input",
		"ExecutionSuspendedEvent.input",
		"ExecutionTerminatedEvent.input",
		"PropagatedHistoryChunk.rawEvents",
		"TaskCompletedEvent.result",
		"TaskFailureDetails.errorMessage",
		"TaskFailureDetails.stackTrace",
		"TaskScheduledEvent.input",
	}

	plain := []string{
		// The attestation payload holds an unsalted SHA-256 over the plaintext
		// input and output (historysigning.IODigest). It must stay readable to
		// verify signatures without the key, so a low-entropy payload can be
		// recovered from it by brute force when signing and encryption are
		// both on.
		"ActivityCompletionAttestation.payload",
		"ActivityCompletionAttestation.signature",
		"ChildCompletionAttestation.payload",
		"ChildCompletionAttestation.signature",
		"ChildWorkflowInstanceCompletedEvent.signerCertificate",
		"ChildWorkflowInstanceCreatedEvent.instanceId",
		"ChildWorkflowInstanceCreatedEvent.name",
		"ChildWorkflowInstanceCreatedEvent.version",
		"ChildWorkflowInstanceFailedEvent.signerCertificate",
		"DetachedWorkflowInstanceCreatedEvent.instanceId",
		"EventRaisedEvent.name",
		"EventSentEvent.instanceId",
		"EventSentEvent.name",
		"ExecutionStalledEvent.description",
		"ExecutionStartedEvent.name",
		"ExecutionStartedEvent.tags",
		"ExecutionStartedEvent.version",
		"ExecutionStartedEvent.workflowSpanID",
		"ParentInstanceInfo.appID",
		"ParentInstanceInfo.appNamespace",
		"ParentInstanceInfo.name",
		"ParentInstanceInfo.version",
		"PropagatedHistoryChunk.appId",
		"PropagatedHistoryChunk.instanceId",
		"PropagatedHistoryChunk.rawSignatures",
		"PropagatedHistoryChunk.signingCertChains",
		"PropagatedHistoryChunk.workflowName",
		"RerunParentInstanceInfo.instanceID",
		"RetryParentInstanceInfo.instanceID",
		"TaskCompletedEvent.signerCertificate",
		"TaskCompletedEvent.taskExecutionId",
		"TaskFailedEvent.signerCertificate",
		"TaskFailedEvent.taskExecutionId",
		"TaskFailureDetails.errorType",
		"TaskRouter.sourceAppID",
		"TaskRouter.targetAppID",
		"TaskRouter.targetAppNamespace",
		"TaskScheduledEvent.name",
		"TaskScheduledEvent.taskExecutionId",
		"TaskScheduledEvent.version",
		"TimerCreatedEvent.name",
		"TimerOriginActivityRetry.taskExecutionId",
		"TimerOriginChildWorkflowRetry.instanceId",
		"TimerOriginExternalEvent.name",
		"TraceContext.spanID",
		"TraceContext.traceParent",
		"TraceContext.traceState",
		"WorkflowInstance.executionId",
		"WorkflowInstance.instanceId",
		"WorkflowVersion.name",
		"WorkflowVersion.patches",
	}

	var gotSealed, gotPlain []string
	seen := make(map[protoreflect.FullName]bool)
	var visit func(md protoreflect.MessageDescriptor)
	visit = func(md protoreflect.MessageDescriptor) {
		if seen[md.FullName()] {
			return
		}
		seen[md.FullName()] = true
		fields := md.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)
			name := string(md.FullName()) + "." + string(fd.Name())
			class := classify(md.FullName(), fd)
			switch {
			case class == descend:
				visit(fd.Message())
			case class != plaintext:
				gotSealed = append(gotSealed, name)
			case fd.IsMap(),
				fd.Kind() == protoreflect.StringKind,
				fd.Kind() == protoreflect.BytesKind,
				fd.Message() != nil && fd.Message().FullName() == stringValueName:
				gotPlain = append(gotPlain, name)
			}
		}
	}
	visit((&protos.HistoryEvent{}).ProtoReflect().Descriptor())
	visit((&protos.PropagatedHistory{}).ProtoReflect().Descriptor())

	sort.Strings(gotSealed)
	sort.Strings(gotPlain)
	assert.Equal(t, sealed, gotSealed, "sealed payload fields changed")
	assert.Equal(t, plain, gotPlain, "a string, bytes or StringValue field was added or removed: decide whether it is a payload and update classify or this list")
}
