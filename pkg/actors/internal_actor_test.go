/*
Copyright 2022 The Dapr Authors
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
package actors

import (
	context "context"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/stretchr/testify/assert"
)

type mockInternalActor struct {
	TestOutput    any
	actorsRuntime Actors
}

type invokeMethodCallInfo struct {
	ActorID    string
	MethodName string
	Input      []byte
	Output     any
}

// DeactivateActor implements InternalActor
func (*mockInternalActor) DeactivateActor(ctx context.Context, actorID string) error {
	panic("unimplemented")
}

// InvokeMethod implements InternalActor
func (ia *mockInternalActor) InvokeMethod(ctx context.Context, actorID string, methodName string, data []byte) (any, error) {
	// Echo all the inputs back to the caller, plus the preconfigured output
	return &invokeMethodCallInfo{
		ActorID:    actorID,
		MethodName: methodName,
		Input:      data,
		Output:     ia.TestOutput,
	}, nil
}

// InvokeReminder implements InternalActor
func (*mockInternalActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, params []byte) error {
	panic("unimplemented")
}

// InvokeTimer implements InternalActor
func (*mockInternalActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	panic("unimplemented")
}

// SetActorRuntime implements InternalActor
func (ia *mockInternalActor) SetActorRuntime(actorsRuntime Actors) {
	ia.actorsRuntime = actorsRuntime
}

// newTestActorsRuntimeWithInternalActors creates and initializes an actors runtime with a specified set of internal actors
func newTestActorsRuntimeWithInternalActors(internalActors map[string]InternalActor) (*actorsRuntime, error) {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	config := NewConfig("", TestAppID, []string{"placement:5050"}, 0, "", config.ApplicationConfig{})
	a := NewActors(store, nil, nil, config, nil, spec, nil, resiliency.New(log), "actorStore", internalActors)
	if err := a.Init(); err != nil {
		return nil, err
	}

	return a.(*actorsRuntime), nil
}

func TestCallInternalActor(t *testing.T) {
	const (
		testActorType = InternalActorTypePrefix + "pet"
		testActorID   = "dog"
		testMethod    = "bite"
		testInput     = "badguy"
		testOutput    = "ouch!"
	)

	internalActors := make(map[string]InternalActor)
	internalActors[testActorType] = &mockInternalActor{TestOutput: testOutput}

	req := invokev1.NewInvokeMethodRequest(testMethod).
		WithActor(testActorType, testActorID).
		WithRawData([]byte(testInput), invokev1.OctetStreamContentType)

	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	if !assert.NoError(t, err) {
		return
	}
	resp, err := testActorRuntime.callLocalActor(context.Background(), req)
	if assert.NoError(t, err) && assert.NotNil(t, resp) {
		// Verify the response metadata matches what we expect
		assert.Equal(t, int32(200), resp.Status().Code)
		contentType, data := resp.RawData()
		assert.Equal(t, invokev1.OctetStreamContentType, contentType)

		// Verify the actor got all the expected inputs (which are echoed back to us)
		info, err := decodeTestResponse(data)
		if !assert.NoError(t, err) || !assert.NotNil(t, info) {
			return
		}
		assert.Equal(t, testActorID, info.ActorID)
		assert.Equal(t, testMethod, info.MethodName)
		assert.Equal(t, []byte(testInput), info.Input)

		// Verify the preconfigured output was successfully returned back to us
		assert.Equal(t, testOutput, info.Output)
	}
}

func decodeTestResponse(data []byte) (*invokeMethodCallInfo, error) {
	info := new(invokeMethodCallInfo)
	if err := DecodeInternalActorResponse(data, info); err != nil {
		return nil, err
	}
	return info, nil
}
