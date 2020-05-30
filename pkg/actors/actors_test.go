// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dapr/components-contrib/state"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	TestAppID   = "fakeAppID"
	TestKeyName = "key0"
)

type fakeStateStore struct {
	items map[string][]byte
	lock  *sync.RWMutex
}

func (f *fakeStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (f *fakeStateStore) Delete(req *state.DeleteRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, req.Key)
	return nil
}

func (f *fakeStateStore) BulkDelete(req []state.DeleteRequest) error {
	return nil
}

func (f *fakeStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	item := f.items[req.Key]
	return &state.GetResponse{Data: item}, nil
}

func (f *fakeStateStore) Set(req *state.SetRequest) error {
	b, _ := json.Marshal(&req.Value)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = b
	return nil
}

func (f *fakeStateStore) BulkSet(req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(reqs []state.TransactionalRequest) error {
	return nil
}

func newTestActorsRuntime() *actorsRuntime {
	mockAppChannel := new(channelt.MockAppChannel)
	spec := config.TracingSpec{SamplingRate: "1"}
	fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	mockAppChannel.On("GetBaseAddress").Return("http://127.0.0.1", nil)
	mockAppChannel.On(
		"InvokeMethod",
		mock.AnythingOfType("*context.emptyCtx"),
		mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil)

	store := fakeStore()
	config := NewConfig("", TestAppID, "", nil, 0, "", "", "", false)
	a := NewActors(store, mockAppChannel, nil, config, nil, spec)

	return a.(*actorsRuntime)
}

func getTestActorTypeAndID() (string, string) {
	return "cat", "e485d5de-de48-45ab-816e-6cc700d18ace"
}

func fakeStore() state.Store {
	return &fakeStateStore{
		items: map[string][]byte{},
		lock:  &sync.RWMutex{},
	}
}

func fakeCallAndActivateActor(actors *actorsRuntime, actorType, actorID string) {
	actorKey := actors.constructCompositeKey(actorType, actorID)
	actors.actorsTable.LoadOrStore(actorKey, newActor(actorType, actorID))
}

func deactivateActorWithDuration(testActorsRuntime *actorsRuntime, actorType, actorID string, actorIdleTimeout time.Duration) {
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)
	scanInterval := time.Second * 1
	testActorsRuntime.startDeactivationTicker(scanInterval, actorIdleTimeout)
}

func createReminderData(actorID, actorType, name, period, dueTime, data string) CreateReminderRequest {
	return CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		Data:      data,
	}
}

func createTimerData(actorID, actorType, name, period, dueTime, callback, data string) CreateTimerRequest {
	return CreateTimerRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		Data:      data,
		Callback:  callback,
	}
}

func TestActorIsDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 2
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorType, actorID, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.False(t, exists)
}

func TestActorIsNotDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 5
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorType, actorID, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.True(t, exists)
}

func TestTimerExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	err := testActorsRuntime.executeTimer(actorType, actorID, "timer1", "2s", "2s", "callback", "data")
	assert.Nil(t, err)
}

func TestTimerExecutionZeroDuration(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	err := testActorsRuntime.executeTimer(actorType, actorID, "timer1", "0ms", "0ms", "callback", "data")
	assert.Nil(t, err)
}

func TestReminderExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	err := testActorsRuntime.executeReminder(actorType, actorID, "2s", "2s", "reminder1", "data")
	assert.Nil(t, err)
}

func TestReminderExecutionZeroDuration(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	err := testActorsRuntime.executeReminder(actorType, actorID, "0ms", "0ms", "reminder0", "data")
	assert.Nil(t, err)
}

func TestSetReminderTrack(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	err := testActorsRuntime.updateReminderTrack(actorType, actorID)
	assert.Nil(t, err)
}

func TestGetReminderTrack(t *testing.T) {
	t.Run("reminder doesn't exist", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.Empty(t, r.LastFiredTime)
	})

	t.Run("reminder exists", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		testActorsRuntime.updateReminderTrack(actorType, actorID)
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.NotEmpty(t, r.LastFiredTime)
	})
}

func TestCreateReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	err := testActorsRuntime.CreateReminder(ctx, &CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder1",
		Period:    "1s",
		DueTime:   "1s",
		Data:      nil,
	})
	assert.Nil(t, err)
}

func TestOverrideReminder(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "b", reminders[0].Data)
	})

	t.Run("override dueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "2s", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].DueTime)
	})

	t.Run("override period", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].Period)
	})
}

func TestDeleteReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "")
	testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))
	err := testActorsRuntime.DeleteReminder(ctx, &DeleteReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)
	assert.Equal(t, 0, len(testActorsRuntime.reminders[actorType]))
}

func TestGetReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "a")
	testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))
	r, err := testActorsRuntime.GetReminder(ctx, &GetReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)
	assert.Equal(t, r.Data, "a")
	assert.Equal(t, r.Period, "1s")
	assert.Equal(t, r.DueTime, "1s")
}

func TestDeleteTimer(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	timer := createTimerData(actorID, actorType, "timer1", "100ms", "100ms", "callback", "")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	assert.Nil(t, err)

	timerKey := testActorsRuntime.constructCompositeKey(actorKey, timer.Name)

	_, ok := testActorsRuntime.activeTimers.Load(timerKey)
	assert.True(t, ok)

	err = testActorsRuntime.DeleteTimer(ctx, &DeleteTimerRequest{
		Name:      timer.Name,
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)

	_, ok = testActorsRuntime.activeTimers.Load(timerKey)
	assert.False(t, ok)
}

func TestReminderFires(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 250)
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)
	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotNil(t, track)
	assert.NotEmpty(t, track.LastFiredTime)
}

func TestReminderDueDate(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "500ms", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Nil(t, err)

	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.Empty(t, track.LastFiredTime)

	time.Sleep(time.Second * 1)

	track, err = testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotEmpty(t, track.LastFiredTime)
}

func TestReminderPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 250)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.NotEmpty(t, track.LastFiredTime)

	time.Sleep(time.Second * 3)

	track2, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotEmpty(t, track2.LastFiredTime)

	assert.NotEqual(t, track.LastFiredTime, track2.LastFiredTime)
}

func TestReminderFiresOnceWithEmptyPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := testActorsRuntime.constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "", "100ms", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 150)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Empty(t, track.LastFiredTime)
}

func TestConstructActorStateKey(t *testing.T) {
	delim := "||"
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	expected := strings.Join([]string{TestAppID, actorType, actorID, TestKeyName}, delim)

	// act
	stateKey := testActorsRuntime.constructActorStateKey(actorType, actorID, TestKeyName)

	// assert
	assert.Equal(t, expected, stateKey)

	// Check split
	keys := strings.Split(stateKey, delim)
	assert.Equal(t, 4, len(keys))
	assert.Equal(t, TestAppID, keys[0])
	assert.Equal(t, actorType, keys[1])
	assert.Equal(t, actorID, keys[2])
	assert.Equal(t, TestKeyName, keys[3])
}

func TestSaveState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	// act
	fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

	err := testActorRuntime.SaveState(ctx, &SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
		Value:     val,
	})
	assert.NoError(t, err)

	// assert
	response, err := testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))
}

func TestGetState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

	testActorRuntime.SaveState(ctx, &SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
		Value:     val,
	})

	// act
	response, err := testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	// assert
	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))
}

func TestDeleteState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	// save test state
	fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

	testActorRuntime.SaveState(ctx, &SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
		Value:     val,
	})

	// make sure that state is stored.
	response, err := testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))

	// act
	err = testActorRuntime.DeleteState(ctx, &DeleteStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})
	assert.NoError(t, err)

	// assert
	response, err = testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	assert.NoError(t, err)
	assert.Nil(t, response.Data)
}

func TestTransactionalState(t *testing.T) {
	ctx := context.Background()
	t.Run("Single set request succeeds", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

		err := testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request: TransactionalUpsert{
						Key:   "key1",
						Value: "fakeData",
					},
				},
			},
		})
		assert.Nil(t, err)
	})

	t.Run("Multiple requests succeeds", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

		err := testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request: TransactionalUpsert{
						Key:   "key1",
						Value: "fakeData",
					},
				},
				{
					Operation: Delete,
					Request: TransactionalDelete{
						Key: "key1",
					},
				},
			},
		})
		assert.Nil(t, err)
	})

	t.Run("Wrong request body - should fail", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

		err := testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request:   "wrongBody",
				},
			},
		})
		assert.NotNil(t, err)
	})

	t.Run("Unsupported operation type - should fail", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

		err := testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: "Wrong",
					Request:   "wrongBody",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Equal(t, "operation type Wrong not supported", err.Error())
	})
}

func TestActiveActorsCount(t *testing.T) {
	ctx := context.Background()
	t.Run("Actors Count", func(t *testing.T) {
		expectedCounts := []ActiveActorsCount{{Type: "cat", Count: 2}, {Type: "dog", Count: 1}}

		testActorRuntime := newTestActorsRuntime()

		fakeCallAndActivateActor(testActorRuntime, "cat", "abcd")
		fakeCallAndActivateActor(testActorRuntime, "cat", "xyz")
		fakeCallAndActivateActor(testActorRuntime, "dog", "xyz")

		actualCounts := testActorRuntime.GetActiveActorsCount(ctx)
		assert.ElementsMatch(t, expectedCounts, actualCounts)
	})

	t.Run("Actors Count empty", func(t *testing.T) {
		expectedCounts := []ActiveActorsCount{}

		testActorRuntime := newTestActorsRuntime()

		actualCounts := testActorRuntime.GetActiveActorsCount(ctx)
		assert.Equal(t, expectedCounts, actualCounts)
	})
}

func TestActorsAppHealthCheck(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	testActorRuntime.config.HostedActorTypes = []string{"actor1"}
	go testActorRuntime.startAppHealthCheck(health.WithFailureThreshold(1), health.WithInterval(1))

	time.Sleep(time.Second * 2)
	assert.False(t, testActorRuntime.appHealthy)
}
