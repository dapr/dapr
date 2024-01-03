/*
Copyright 2023 The Dapr Authors
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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/security/fake"
)

type mockInternalActor struct {
	TestOutput        any
	InvokedReminders  []InternalActorReminder
	DeactivationCalls []string
	actorsRuntime     Actors
}

type invokeMethodCallInfo struct {
	ActorID    string
	MethodName string
	Input      []byte
	Output     any
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
func (ia *mockInternalActor) InvokeMethod(ctx context.Context, actorID string, methodName string, data []byte, metadata map[string][]string) ([]byte, error) {
	// Echo all the inputs back to the caller, plus the preconfigured output
	return EncodeInternalActorData(&invokeMethodCallInfo{
		ActorID:    actorID,
		MethodName: methodName,
		Input:      data,
		Output:     ia.TestOutput,
	})
}

// InvokeReminder implements InternalActor
func (ia *mockInternalActor) InvokeReminder(ctx context.Context, actorID string, reminder InternalActorReminder, metadata map[string][]string) error {
	ia.InvokedReminders = append(ia.InvokedReminders, reminder)
	return nil
}

// InvokeTimer implements InternalActor
func (*mockInternalActor) InvokeTimer(ctx context.Context, actorID string, timer InternalActorReminder, metadata map[string][]string) error {
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
	})

	compStore := compstore.New()
	compStore.AddStateStore("actorStore", store)
	a := NewActors(ActorsOpts{
		CompStore:      compStore,
		Config:         config,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
		Security:       fake.New(),
		MockPlacement:  NewMockPlacement(TestAppID),
	})

	for actorType, actor := range internalActors {
		if err := a.RegisterInternalActor(context.TODO(), actorType, actor, 0); err != nil {
			return nil, err
		}
	}

	if err := a.Init(context.Background()); err != nil {
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
	require.NoError(t, err)

	// Need this nolint due to a bug in the linter
	//nolint:protogetter
	req := internals.NewInternalInvokeRequest(testMethod).
		WithActor(testActorType, testActorID).
		WithData([]byte(testInput)).
		WithContentType(invokev1.OctetStreamContentType)

	resp, err := testActorRuntime.callInternalActor(context.Background(), req, internalActors[testActorType])
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify the response metadata matches what we expect
	assert.Equal(t, int32(200), resp.GetStatus().GetCode())

	// Verify the actor got all the expected inputs (which are echoed back to us)
	info, err := decodeTestResponse(bytes.NewReader(resp.GetMessage().GetData().GetValue()))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, testActorID, info.ActorID)
	assert.Equal(t, testMethod, info.MethodName)
	assert.Equal(t, []byte(testInput), info.Input)

	// Verify the preconfigured output was successfully returned back to us
	assert.Equal(t, testOutput, info.Output)
}

func TestInternalActorReminder(t *testing.T) {
	const testActorType = InternalActorTypePrefix + "test"
	ia := &mockInternalActor{}
	internalActors := make(map[string]InternalActor)
	internalActors[testActorType] = ia
	testActorRuntime, err := newTestActorsRuntimeWithInternalActors(internalActors)
	require.NoError(t, err)

	period, _ := internal.NewReminderPeriod("2s")
	data, _ := json.Marshal(testReminderData{
		SomeBytes:  []byte("こんにちは！"),
		SomeInt:    42,
		SomeString: "Hello!",
	})
	testReminder := &internal.Reminder{
		ActorType:      testActorType,
		ActorID:        "myActor",
		RegisteredTime: time.Now().Add(2 * time.Second),
		DueTime:        "2s",
		Period:         period,
		Name:           "reminder1",
		Data:           data,
	}
	err = testActorRuntime.doExecuteReminderOrTimer(context.Background(), testReminder, false)
	require.NoError(t, err)
	require.Len(t, ia.InvokedReminders, 1)
	invokedReminder := ia.InvokedReminders[0]
	assert.Equal(t, testReminder.Name, invokedReminder.Name)
	assert.Equal(t, testReminder.DueTime, invokedReminder.DueTime)
	assert.Equal(t, testReminder.Period.String(), invokedReminder.Period)

	// Reminder data gets marshaled to JSON and unmarshaled back to map[string]interface{}
	var actualData testReminderData
	err = invokedReminder.DecodeData(&actualData)
	require.NoError(t, err)
	enc, err := json.Marshal(actualData)
	require.NoError(t, err)
	assert.Equal(t, []byte(testReminder.Data), enc)
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
	require.NoError(t, err)

	// Call the internal actor to "activate" it
	req := internals.
		NewInternalInvokeRequest("Foo").
		WithActor(testActorType, testActorID)

	_, err = testActorRuntime.callInternalActor(context.Background(), req, ia)
	require.NoError(t, err)

	// Deactivate the actor, ensuring no errors and that the correct actor ID was provided.
	actAny, ok := testActorRuntime.actorsTable.Load(constructCompositeKey(testActorType, testActorID))
	require.True(t, ok)
	act, ok := actAny.(*actor)
	require.True(t, ok)
	err = testActorRuntime.deactivateActor(act)
	require.NoError(t, err)
	if assert.Len(t, ia.DeactivationCalls, 1) {
		assert.Equal(t, testActorID, ia.DeactivationCalls[0])
	}
}

func decodeTestResponse(data io.Reader) (*invokeMethodCallInfo, error) {
	info := new(invokeMethodCallInfo)
	err := DecodeInternalActorData(data, info)
	if err != nil {
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
	require.NoError(t, err)
	actorCounts := testActorRuntime.getActiveActorsCount(context.Background())
	assert.Empty(t, actorCounts)
}
