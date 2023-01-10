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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

type mockInternalActor struct {
	TestOutput        any
	InvokedReminders  []*reminder
	DeactivationCalls []string
	actorsRuntime     Actors
}

type invokeMethodCallInfo struct {
	ActorID    string
	MethodName string
	Input      []byte
	Output     any
}

type reminder struct {
	ActorID string
	Name    string
	Data    []byte
	DueTime string
	Period  string
}

type testReminderData struct {
	SomeBytes  []byte `json:"someBytes"`
	SomeInt    int64  `json:"someInt"`
	SomeString string `json:"someString"`
}

// DeactivateActor implements InternalActor
func (ia *mockInternalActor) DeactivateActor(ctx context.Context, actorID string) error {
	ia.DeactivationCalls = append(ia.DeactivationCalls, actorID)
	return nil
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
func (ia *mockInternalActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, data []byte, dueTime string, period string) error {
	r := &reminder{
		Name:    reminderName,
		ActorID: actorID,
		Data:    data,
		DueTime: dueTime,
		Period:  period,
	}
	ia.InvokedReminders = append(ia.InvokedReminders, r)
	return nil
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
	config := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	a := NewActors(ActorsOpts{
		StateStore:     store,
		Config:         config,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
	})

	for actorType, actor := range internalActors {
		if err := a.RegisterInternalActor(context.TODO(), actorType, actor); err != nil {
			return nil, err
		}
	}

	if err := a.Init(); err != nil {
		return nil, err
	}

	return a.(*actorsRuntime), nil
}

func TestInternalActorCall(t *testing.T) {
	const (
		testActorType = InternalActorTypePrefix + "pet"
		testActorID   = "dog"
		testMethod    = "bite"
		testInput     = "badguy"
		testOutput    = "ouch!"
	)

	internalActors := make(map[string]InternalActor)
	internalActors[testActorType] = &mockInternalActor{TestOutput: testOutput}
	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	if !assert.NoError(t, err) {
		return
	}

	req := invokev1.NewInvokeMethodRequest(testMethod).
		WithActor(testActorType, testActorID).
		WithRawData([]byte(testInput), invokev1.OctetStreamContentType)
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

func TestInternalActorReminder(t *testing.T) {
	const testActorType = InternalActorTypePrefix + "test"
	ia := &mockInternalActor{}
	internalActors := make(map[string]InternalActor)
	internalActors[testActorType] = ia
	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	if !assert.NoError(t, err) {
		return
	}

	testReminder := &Reminder{
		ActorType: testActorType,
		ActorID:   "myActor",
		DueTime:   "2s",
		Period:    "2s",
		Name:      "reminder1",
		Data: testReminderData{
			SomeBytes:  []byte("こんにちは！"),
			SomeInt:    42,
			SomeString: "Hello!",
		},
	}
	if err = testActorRuntime.executeReminder(testReminder); !assert.NoError(t, err) {
		return
	}
	if !assert.Len(t, ia.InvokedReminders, 1) {
		return
	}
	invokedReminder := ia.InvokedReminders[0]
	assert.Equal(t, testReminder.ActorID, invokedReminder.ActorID)
	assert.Equal(t, testReminder.Name, invokedReminder.Name)
	assert.Equal(t, testReminder.DueTime, invokedReminder.DueTime)
	assert.Equal(t, testReminder.Period, invokedReminder.Period)

	// Reminder data gets marshaled to JSON and unmarshaled back to map[string]interface{}
	var actualData testReminderData
	DecodeInternalActorReminderData(invokedReminder.Data, &actualData)
	assert.Equal(t, testReminder.Data, actualData)
}

func TestInternalActorDeactivation(t *testing.T) {
	const (
		testActorType = InternalActorTypePrefix + "test"
		testActorID   = "foo"
	)
	ia := &mockInternalActor{}
	internalActors := make(map[string]InternalActor)
	internalActors[testActorType] = ia
	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	if !assert.NoError(t, err) {
		return
	}

	// Call the internal actor to "activate" it
	req := invokev1.NewInvokeMethodRequest("Foo").WithActor(testActorType, testActorID)
	_, err = testActorRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)

	// Deactivate the actor, ensuring no errors and that the correct actor ID was provided.
	if err = testActorRuntime.deactivateActor(testActorType, testActorID); assert.NoError(t, err) {
		if assert.Len(t, ia.DeactivationCalls, 1) {
			assert.Equal(t, testActorID, ia.DeactivationCalls[0])
		}
	}
}

func decodeTestResponse(data []byte) (*invokeMethodCallInfo, error) {
	info := new(invokeMethodCallInfo)
	if err := DecodeInternalActorData(data, info); err != nil {
		return nil, err
	}
	return info, nil
}

// TestInternalActorsNotCounted verifies that internal actors are not counted in the
// GetActiveActorsCount API, which should only include counts of user-defined actors.
func TestInternalActorsNotCounted(t *testing.T) {
	internalActors := make(map[string]InternalActor)
	internalActors[InternalActorTypePrefix+"wfengine.workflow"] = &mockInternalActor{}
	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	if !assert.NoError(t, err) {
		return
	}
	actorCounts := testActorRuntime.GetActiveActorsCount(context.Background())
	assert.Empty(t, actorCounts)
}
