// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
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
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	TestAppID   = "fakeAppID"
	TestKeyName = "key0"
)

// testRequest is the request object that encapsulates the `data` field of a request
type testRequest struct {
	Data interface{} `json:"data"`
}

type mockAppChannel struct {
	channel.AppChannel
	requestC chan testRequest
}

func (m *mockAppChannel) GetBaseAddress() string {
	return "http://127.0.0.1"
}

func (m *mockAppChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if m.requestC != nil {
		var request testRequest
		if err := json.Unmarshal(req.Message().Data.Value, &request); err == nil {
			m.requestC <- request
		}
	}

	return invokev1.NewInvokeMethodResponse(200, "OK", nil), nil
}

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

func (f *fakeStateStore) BulkGet(req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	return false, nil, nil
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

func (f *fakeStateStore) Multi(request *state.TransactionalStateRequest) error {
	for _, o := range request.Operations {
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			b, _ := json.Marshal(req.Value)
			f.items[req.Key] = b
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			delete(f.items, req.Key)
		}
	}

	return nil
}

func newTestActorsRuntimeWithMock(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	config := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "")
	a := NewActors(store, appChannel, nil, config, nil, spec)

	return a.(*actorsRuntime)
}

func newTestActorsRuntime() *actorsRuntime {
	appChannel := new(mockAppChannel)

	return newTestActorsRuntimeWithMock(appChannel)
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
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType)
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
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType)
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
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].Period)
	})
}

func TestOverrideReminderCancelsActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		appChannel := mockAppChannel{
			requestC: requestC,
		}
		testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "1s", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, reminderName, "9s", "1s", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		// Check reminder is updated
		assert.Equal(t, "9s", reminders[0].Period)
		assert.Equal(t, "1s", reminders[0].DueTime)
		assert.Equal(t, "b", reminders[0].Data)

		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "2s", "c")
		testActorsRuntime.CreateReminder(ctx, &reminder3)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		// Check reminder is updated
		assert.Equal(t, "8s", reminders[0].Period)
		assert.Equal(t, "2s", reminders[0].DueTime)
		assert.Equal(t, "c", reminders[0].Data)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].Data, request.Data)
		case <-time.After(15 * time.Second):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestOverrideReminderCancelsMultipleActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		appChannel := mockAppChannel{
			requestC: requestC,
		}
		testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "3s", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		time.Sleep(50 * time.Millisecond)

		reminder2 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "b")
		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "c")
		go testActorsRuntime.CreateReminder(ctx, &reminder2)
		go testActorsRuntime.CreateReminder(ctx, &reminder3)

		time.Sleep(2 * time.Second)

		// Check reminder is updated
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		// The statestore could have either reminder2 or reminder3 based on the timing.
		// Therefore, not verifying data field
		assert.Equal(t, "8s", reminders[0].Period)
		assert.Equal(t, "4s", reminders[0].DueTime)

		time.Sleep(50 * time.Millisecond)

		reminder4 := createReminderData(actorID, actorType, reminderName, "7s", "2s", "d")
		testActorsRuntime.CreateReminder(ctx, &reminder4)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].Data, request.Data)

			// Check reminder is updated
			assert.Equal(t, "7s", reminders[0].Period)
			assert.Equal(t, "2s", reminders[0].DueTime)
			assert.Equal(t, "d", reminders[0].Data)
		case <-time.After(15 * time.Second):
			assert.Fail(t, "request channel timed out")
		}
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

func TestOverrideTimerCancelsActiveTimers(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		appChannel := mockAppChannel{
			requestC: requestC,
		}
		testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
		actorType, actorID := getTestActorTypeAndID()
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)
		timerName := "timer1"

		timer := createTimerData(actorID, actorType, timerName, "10s", "1s", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.Nil(t, err)

		timer2 := createTimerData(actorID, actorType, timerName, "9s", "1s", "callback2", "b")
		testActorsRuntime.CreateTimer(ctx, &timer2)

		timer3 := createTimerData(actorID, actorType, timerName, "8s", "2s", "callback3", "c")
		testActorsRuntime.CreateTimer(ctx, &timer3)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, timer3.Data, request.Data)
		case <-time.After(15 * time.Second):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestOverrideTimerCancelsMultipleActiveTimers(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		appChannel := mockAppChannel{
			requestC: requestC,
		}
		testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
		actorType, actorID := getTestActorTypeAndID()
		timerName := "timer1"
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

		timer := createTimerData(actorID, actorType, timerName, "10s", "3s", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.Nil(t, err)

		time.Sleep(50 * time.Millisecond)

		timer2 := createTimerData(actorID, actorType, timerName, "8s", "4s", "callback2", "b")
		timer3 := createTimerData(actorID, actorType, timerName, "8s", "4s", "callback3", "c")
		go testActorsRuntime.CreateTimer(ctx, &timer2)
		go testActorsRuntime.CreateTimer(ctx, &timer3)

		time.Sleep(2 * time.Second)

		timer4 := createTimerData(actorID, actorType, timerName, "7s", "2s", "callback4", "d")
		testActorsRuntime.CreateTimer(ctx, &timer4)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, timer4.Data, request.Data)
		case <-time.After(15 * time.Second):
			assert.Fail(t, "request channel timed out")
		}
	})
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

func TestGetState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

	testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Operations: []TransactionalOperation{
			{
				Operation: Upsert,
				Request: TransactionalUpsert{
					Key:   TestKeyName,
					Value: val,
				},
			},
		},
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

	fakeCallAndActivateActor(testActorRuntime, actorType, actorID)

	// insert state
	testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Operations: []TransactionalOperation{
			{
				Operation: Upsert,
				Request: TransactionalUpsert{
					Key:   TestKeyName,
					Value: val,
				},
			},
		},
	})

	// save state
	response, err := testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	// make sure that state is stored.
	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))

	// delete state
	testActorRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Operations: []TransactionalOperation{
			{
				Operation: Delete,
				Request: TransactionalUpsert{
					Key: TestKeyName,
				},
			},
		},
	})

	// act
	response, err = testActorRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	// assert
	assert.NoError(t, err)
	assert.Nil(t, response.Data)
}

func TestCallLocalActor(t *testing.T) {
	const (
		testActorType = "pet"
		testActorID   = "dog"
		testMethod    = "bite"
	)

	req := invokev1.NewInvokeMethodRequest(testMethod).WithActor(testActorType, testActorID)

	t.Run("invoke actor successfully", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		resp, err := testActorRuntime.callLocalActor(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("actor is already disposed", func(t *testing.T) {
		// arrange
		testActorRuntime := newTestActorsRuntime()
		actorKey := testActorRuntime.constructCompositeKey(testActorType, testActorID)
		act := newActor(testActorType, testActorID)

		// add test actor
		testActorRuntime.actorsTable.LoadOrStore(actorKey, act)
		act.lock()
		assert.True(t, act.isBusy())

		// get dispose channel for test actor
		ch := act.channel()
		act.unlock()

		_, closed := <-ch
		assert.False(t, closed, "dispose channel must be closed after unlock")

		// act
		resp, err := testActorRuntime.callLocalActor(context.Background(), req)

		// assert
		s, _ := status.FromError(err)
		assert.Equal(t, codes.ResourceExhausted, s.Code())
		assert.Nil(t, resp)
	})
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

func TestGetOrCreateActor(t *testing.T) {
	const testActorType = "fakeActor"
	testActorRuntime := newTestActorsRuntime()

	t.Run("create new key", func(t *testing.T) {
		act := testActorRuntime.getOrCreateActor(testActorType, "id-1")
		assert.NotNil(t, act)
	})

	t.Run("try to create the same key", func(t *testing.T) {
		oldActor := testActorRuntime.getOrCreateActor(testActorType, "id-2")
		assert.NotNil(t, oldActor)
		newActor := testActorRuntime.getOrCreateActor(testActorType, "id-2")
		assert.Same(t, oldActor, newActor, "should not create new actor")
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
	go testActorRuntime.startAppHealthCheck(
		health.WithFailureThreshold(1),
		health.WithInterval(1*time.Second),
		health.WithRequestTimeout(100*time.Millisecond))

	time.Sleep(time.Second * 2)
	assert.False(t, testActorRuntime.appHealthy)
}

func TestShutdown(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()

	t.Run("no panic when placement is nil", func(t *testing.T) {
		testActorRuntime.placement = nil
		testActorRuntime.Stop()
		// No panic
	})
}

func TestConstructCompositeKeyWithThreeArgs(t *testing.T) {
	appID := "myapp"
	actorType := "TestActor"
	actorID := "abc123"

	actorsRuntime := newTestActorsRuntime()
	actorKey := actorsRuntime.constructCompositeKey(appID, actorType, actorID)

	assert.Equal(t, "myapp||TestActor||abc123", actorKey)
}

func TestConfig(t *testing.T) {
	c := NewConfig("localhost:5050", "app1", []string{"placement:5050"}, []string{"1"}, 3500, "1s", "2s", "3s", true, "default")
	assert.Equal(t, "localhost:5050", c.HostAddress)
	assert.Equal(t, "app1", c.AppID)
	assert.Equal(t, []string{"placement:5050"}, c.PlacementAddresses)
	assert.Equal(t, []string{"1"}, c.HostedActorTypes)
	assert.Equal(t, 3500, c.Port)
	assert.Equal(t, "1s", c.ActorDeactivationScanInterval.String())
	assert.Equal(t, "2s", c.ActorIdleTimeout.String())
	assert.Equal(t, "3s", c.DrainOngoingCallTimeout.String())
	assert.Equal(t, true, c.DrainRebalancedActors)
	assert.Equal(t, "default", c.Namespace)
}

func TestHostValidation(t *testing.T) {
	t.Run("kubernetes mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "")
		assert.Error(t, err)
	})

	t.Run("kubernetes mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.KubernetesMode, "")
		assert.NoError(t, err)
	})

	t.Run("kubernetes mode with mTLS and namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "default")
		assert.NoError(t, err)
	})

	t.Run("self hosted mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.StandaloneMode, "")
		assert.NoError(t, err)
	})

	t.Run("self hosted mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.StandaloneMode, "")
		assert.NoError(t, err)
	})
}
