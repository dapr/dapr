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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
)

func TestCascadeTerminateEventPreservesInput(t *testing.T) {
	input := wrapperspb.String("cancelled by parent workflow")

	event := cascadeTerminateEvent(&protos.ExecutionTerminatedEvent{
		Input:   input,
		Recurse: true,
	})

	terminated := event.GetExecutionTerminated()
	require.NotNil(t, terminated)
	assert.Same(t, input, terminated.GetInput())
	assert.True(t, terminated.GetRecurse())
}

func TestCascadeTerminateEventPreservesExplicitEmptyInput(t *testing.T) {
	input := wrapperspb.String("")

	event := cascadeTerminateEvent(&protos.ExecutionTerminatedEvent{
		Input:   input,
		Recurse: true,
	})

	terminated := event.GetExecutionTerminated()
	require.NotNil(t, terminated)
	assert.Same(t, input, terminated.GetInput())
	assert.Empty(t, terminated.GetInput().GetValue())
	assert.True(t, terminated.GetRecurse())
}

func TestCascadeTerminateEventPreservesNilInput(t *testing.T) {
	event := cascadeTerminateEvent(&protos.ExecutionTerminatedEvent{
		Recurse: true,
	})

	terminated := event.GetExecutionTerminated()
	require.NotNil(t, terminated)
	assert.Nil(t, terminated.GetInput())
	assert.True(t, terminated.GetRecurse())
}
