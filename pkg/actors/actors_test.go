// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
)

const (
	TestAppID                       = "fakeAppID"
	TestKeyName                     = "key0"
	TestActorMetadataPartitionCount = 3
)

// testRequest is the request object that encapsulates the `data` field of a request.
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

type reentrantAppChannel struct {
	channel.AppChannel
	nextCall []*invokev1.InvokeMethodRequest
	callLog  []string
	a        *actorsRuntime
}

func (r *reentrantAppChannel) GetBaseAddress() string {
	return "http://127.0.0.1"
}

func (r *reentrantAppChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	r.callLog = append(r.callLog, fmt.Sprintf("Entering %s", req.Message().Method))
	if len(r.nextCall) > 0 {
		nextReq := r.nextCall[0]
		r.nextCall = r.nextCall[1:]

		if val, ok := req.Metadata()["Dapr-Reentrancy-Id"]; ok {
			header := fasthttp.RequestHeader{}
			header.Add("Dapr-Reentrancy-Id", val.Values[0])
			nextReq.AddHeaders(&header)
		}
		_, err := r.a.callLocalActor(context.Background(), nextReq)
		if err != nil {
			return nil, err
		}
	}
	r.callLog = append(r.callLog, fmt.Sprintf("Exiting %s", req.Message().Method))

	return invokev1.NewInvokeMethodResponse(200, "OK", nil), nil
}

type fakeStateStoreItem struct {
	data []byte
	etag *string
}

type fakeStateStore struct {
	items map[string]*fakeStateStoreItem
	lock  *sync.RWMutex
}

func (f *fakeStateStore) newItem(data []byte) *fakeStateStoreItem {
	etag, _ := uuid.NewRandom()
	etagString := etag.String()
	return &fakeStateStoreItem{
		data: data,
		etag: &etagString,
	}
}

func (f *fakeStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (f *fakeStateStore) Ping() error {
	return nil
}

func (f *fakeStateStore) Features() []state.Feature {
	return []state.Feature{state.FeatureETag, state.FeatureTransactional}
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

	if item == nil {
		return &state.GetResponse{Data: nil, ETag: nil}, nil
	}

	return &state.GetResponse{Data: item.data, ETag: item.etag}, nil
}

func (f *fakeStateStore) BulkGet(req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	res := []state.BulkGetResponse{}
	for _, oneRequest := range req {
		oneResponse, err := f.Get(&state.GetRequest{
			Key:      oneRequest.Key,
			Metadata: oneRequest.Metadata,
			Options:  oneRequest.Options,
		})
		if err != nil {
			return false, nil, err
		}

		res = append(res, state.BulkGetResponse{
			Key:  oneRequest.Key,
			Data: oneResponse.Data,
			ETag: oneResponse.ETag,
		})
	}

	return true, res, nil
}

func (f *fakeStateStore) Set(req *state.SetRequest) error {
	b, _ := json.Marshal(&req.Value)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = f.newItem(b)

	return nil
}

func (f *fakeStateStore) BulkSet(req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(request *state.TransactionalStateRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	// First we check all eTags
	for _, o := range request.Operations {
		var eTag *string
		key := ""
		if o.Operation == state.Upsert {
			key = o.Request.(state.SetRequest).Key
			eTag = o.Request.(state.SetRequest).ETag
		} else if o.Operation == state.Delete {
			key = o.Request.(state.DeleteRequest).Key
			eTag = o.Request.(state.DeleteRequest).ETag
		}
		item := f.items[key]
		if eTag != nil && item != nil {
			if *eTag != *item.etag {
				return fmt.Errorf("etag does not match for key %v", key)
			}
		}
		if eTag != nil && item == nil {
			return fmt.Errorf("etag does not match for key not found %v", key)
		}
	}

	// Now we can perform the operation.
	for _, o := range request.Operations {
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			b, _ := json.Marshal(req.Value)
			f.items[req.Key] = f.newItem(b)
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			delete(f.items, req.Key)
		}
	}

	return nil
}

type runtimeBuilder struct {
	appChannel  channel.AppChannel
	config      *Config
	featureSpec []config.FeatureSpec
}

func (b *runtimeBuilder) buildActorRuntime() *actorsRuntime {
	if b.appChannel == nil {
		b.appChannel = new(mockAppChannel)
	}

	if b.config == nil {
		config := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{}, 0)
		b.config = &config
	}

	if b.featureSpec == nil {
		b.featureSpec = []config.FeatureSpec{}
	}

	tracingSpec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()

	a := NewActors(store, b.appChannel, nil, *b.config, nil, tracingSpec, b.featureSpec)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMock(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	config := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{}, 0)
	a := NewActors(store, appChannel, nil, config, nil, spec, nil)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	c := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{},
		TestActorMetadataPartitionCount)
	a := NewActors(store, appChannel, nil, c, nil, spec, []config.FeatureSpec{
		{
			Name:    config.ActorTypeMetadata,
			Enabled: true,
		},
	})

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
		items: map[string]*fakeStateStoreItem{},
		lock:  &sync.RWMutex{},
	}
}

func fakeCallAndActivateActor(actors *actorsRuntime, actorType, actorID string) {
	actorKey := constructCompositeKey(actorType, actorID)
	actors.actorsTable.LoadOrStore(actorKey, newActor(actorType, actorID, &reentrancyStackDepth))
}

func deactivateActorWithDuration(testActorsRuntime *actorsRuntime, actorType, actorID string, actorIdleTimeout time.Duration) {
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)
	scanInterval := time.Second * 1
	testActorsRuntime.startDeactivationTicker(scanInterval, actorIdleTimeout)
}

func createReminderData(actorID, actorType, name, period, dueTime, ttl, data string) CreateReminderRequest {
	return CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		TTL:       ttl,
		Data:      data,
	}
}

func createTimerData(actorID, actorType, name, period, dueTime, ttl, callback, data string) CreateTimerRequest {
	return CreateTimerRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		TTL:       ttl,
		Data:      data,
		Callback:  callback,
	}
}

func TestActorIsDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 2
	actorType, actorID := getTestActorTypeAndID()
	actorKey := constructCompositeKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorType, actorID, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.False(t, exists)
}

func TestActorIsNotDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 5
	actorType, actorID := getTestActorTypeAndID()
	actorKey := constructCompositeKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorType, actorID, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.True(t, exists)
}

func TestStoreIsNotInited(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	testActorsRuntime.store = nil

	t.Run("getReminderTrack", func(t *testing.T) {
		r, e := testActorsRuntime.getReminderTrack("foo", "bar")
		assert.NotNil(t, e)
		assert.Nil(t, r)
	})

	t.Run("updateReminderTrack", func(t *testing.T) {
		e := testActorsRuntime.updateReminderTrack("foo", "bar", 1, time.Now())
		assert.NotNil(t, e)
	})

	t.Run("CreateReminder", func(t *testing.T) {
		e := testActorsRuntime.CreateReminder(context.Background(), &CreateReminderRequest{})
		assert.NotNil(t, e)
	})

	t.Run("getRemindersForActorType", func(t *testing.T) {
		r1, r2, e := testActorsRuntime.getRemindersForActorType("foo", false)
		assert.Nil(t, r1)
		assert.Nil(t, r2)
		assert.NotNil(t, e)
	})

	t.Run("DeleteReminder", func(t *testing.T) {
		e := testActorsRuntime.DeleteReminder(context.Background(), &DeleteReminderRequest{})
		assert.NotNil(t, e)
	})
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
	reminder := &Reminder{
		ActorType: actorType,
		ActorID:   actorID,
		DueTime:   "2s",
		Period:    "2s",
		Name:      "reminder1",
		Data:      "data",
	}
	err := testActorsRuntime.executeReminder(reminder)
	assert.Nil(t, err)
}

func TestReminderExecutionZeroDuration(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)
	reminder := &Reminder{
		ActorType: actorType,
		ActorID:   actorID,
		DueTime:   "0ms",
		Period:    "0ms",
		Name:      "reminder0",
		Data:      "data",
	}
	err := testActorsRuntime.executeReminder(reminder)
	assert.Nil(t, err)
}

func TestSetReminderTrack(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	noRepetition := -1
	err := testActorsRuntime.updateReminderTrack(actorType, actorID, noRepetition, time.Now())
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
		repetition := 10
		now := time.Now()
		testActorsRuntime.updateReminderTrack(actorType, actorID, repetition, now)
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.NotEmpty(t, r.LastFiredTime)
		assert.Equal(t, repetition, r.RepetitionLeft)
		assert.Equal(t, now.Format(time.RFC3339), r.LastFiredTime)
	})
}

func TestCreateReminder(t *testing.T) {
	numReminders := 100
	appChannel := new(mockAppChannel)
	testActorsRuntime := newTestActorsRuntimeWithMock(appChannel)
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	err := testActorsRuntime.CreateReminder(ctx, &CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder0",
		Period:    "1s",
		DueTime:   "1s",
		TTL:       "PT10M",
		Data:      nil,
	})
	assert.Nil(t, err)

	// Now creates new reminders and migrates the previous one.
	testActorsRuntimeWithPartition := newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel)
	testActorsRuntimeWithPartition.store = testActorsRuntime.store
	testActorsRuntimeWithPartition.transactionalStore = testActorsRuntime.transactionalStore
	for i := 1; i < numReminders; i++ {
		err = testActorsRuntimeWithPartition.CreateReminder(ctx, &CreateReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			Name:      "reminder" + strconv.Itoa(i),
			Period:    "1s",
			DueTime:   "1s",
			TTL:       "10m",
			Data:      nil,
		})
		assert.Nil(t, err)
	}

	reminderReferences, actorTypeMetadata, err := testActorsRuntimeWithPartition.getRemindersForActorType(actorType, false)
	assert.Nil(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, TestActorMetadataPartitionCount, actorTypeMetadata.RemindersMetadata.PartitionCount)

	partitions := map[uint32]bool{}
	reminders := map[string]bool{}
	for _, reminderRef := range reminderReferences {
		partition := reminderRef.actorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.reminder.Name] = true
		assert.Equal(t, actorTypeMetadata.ID, reminderRef.actorMetadataID)
	}
	assert.Equal(t, TestActorMetadataPartitionCount, len(partitions))
	assert.Equal(t, numReminders, len(reminderReferences))
	assert.Equal(t, numReminders, len(reminders))
}

func TestOverrideReminder(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		assert.Equal(t, "b", reminders[0].reminder.Data)
	})

	t.Run("override dueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "2s", "", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].reminder.DueTime)
	})

	t.Run("override period", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].reminder.Period)
	})

	t.Run("override TTL", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "PT5M", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		ttl := "9999-09-01T00:00:00Z"
		origTime, err := time.Parse(time.RFC3339, ttl)
		assert.Nil(t, err)
		reminder2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", ttl, "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		require.NotEmpty(t, reminders)
		newTime, err := time.Parse(time.RFC3339, reminders[0].reminder.ExpirationTime)
		assert.Nil(t, err)
		assert.LessOrEqual(t, newTime.Sub(origTime), 2*time.Second)
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

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "1s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		reminder2 := createReminderData(actorID, actorType, reminderName, "9s", "1s", "", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		// Check reminder is updated
		assert.Equal(t, "9s", reminders[0].reminder.Period)
		assert.Equal(t, "1s", reminders[0].reminder.DueTime)
		assert.Equal(t, "b", reminders[0].reminder.Data)

		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "2s", "", "c")
		testActorsRuntime.CreateReminder(ctx, &reminder3)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		// Check reminder is updated
		assert.Equal(t, "8s", reminders[0].reminder.Period)
		assert.Equal(t, "2s", reminders[0].reminder.DueTime)
		assert.Equal(t, "c", reminders[0].reminder.Data)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].reminder.Data, request.Data)
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

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "3s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.Nil(t, err)

		time.Sleep(50 * time.Millisecond)

		reminder2 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "b")
		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "c")
		go testActorsRuntime.CreateReminder(ctx, &reminder2)
		go testActorsRuntime.CreateReminder(ctx, &reminder3)

		time.Sleep(2 * time.Second)

		// Check reminder is updated
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)
		// The statestore could have either reminder2 or reminder3 based on the timing.
		// Therefore, not verifying data field
		assert.Equal(t, "8s", reminders[0].reminder.Period)
		assert.Equal(t, "4s", reminders[0].reminder.DueTime)

		time.Sleep(50 * time.Millisecond)

		reminder4 := createReminderData(actorID, actorType, reminderName, "7s", "2s", "", "d")
		testActorsRuntime.CreateReminder(ctx, &reminder4)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.Nil(t, err)

		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].reminder.Data, request.Data)

			// Check reminder is updated
			assert.Equal(t, "7s", reminders[0].reminder.Period)
			assert.Equal(t, "2s", reminders[0].reminder.DueTime)
			assert.Equal(t, "d", reminders[0].reminder.Data)
		case <-time.After(15 * time.Second):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestDeleteReminder(t *testing.T) {
	appChannel := new(mockAppChannel)
	testActorsRuntime := newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel)
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
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

func TestDeleteReminderWithPartitions(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
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

func reminderRepeats(ctx context.Context, t *testing.T, dueTime, period, ttl string, repeats int, timeout, del time.Duration) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	reminder := createReminderData(actorID, actorType, "reminder1", period, dueTime, ttl, "data")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	if repeats == 0 {
		assert.EqualError(t, err, "reminder reminder1 has zero repetitions")
		return
	}
	assert.NoError(t, err)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))

	cnt := 0
	var (
		delTimer  *time.Timer
		delTimerC <-chan time.Time
	)
	exitTimer := time.NewTimer(timeout)
	if del > 0 {
		delTimer = time.NewTimer(del)
		delTimerC = delTimer.C
	}
	defer func() {
		exitTimer.Stop()
		if delTimer != nil {
			delTimer.Stop()
		}
	}()
L:
	for {
		select {
		case request := <-requestC:
			assert.Equal(t, reminder.Data, request.Data)
			cnt++
			if cnt > repeats {
				break L
			}
		case <-delTimerC:
			testActorsRuntime.DeleteReminder(ctx, &DeleteReminderRequest{
				Name:      reminder.Name,
				ActorID:   reminder.ActorID,
				ActorType: reminder.ActorType,
			})
		case <-exitTimer.C:
			break L
		}
	}
	assert.Equal(t, repeats, cnt)
}

func TestReminderRepeats(t *testing.T) {
	ctx := context.Background()
	t.Run("reminder with dueTime is ignored", func(t *testing.T) {
		reminderRepeats(ctx, t, "2s", "R0/PT2S", "", 0, 0, 0)
	})
	t.Run("reminder without dueTime is ignored", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "R0/PT2S", "", 0, 0, 0)
	})
	t.Run("reminder with dueTime repeats once", func(t *testing.T) {
		reminderRepeats(ctx, t, "2s", "R1/PT2S", "", 1, 6*time.Second, 0)
	})
	t.Run("reminder without dueTime repeats once", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "R1/PT2S", "", 1, 4*time.Second, 0)
	})
	t.Run("reminder with dueTime repeats not set", func(t *testing.T) {
		reminderRepeats(ctx, t, "2s", "", "", 1, 6*time.Second, 0)
	})
	t.Run("reminder without dueTime repeats not set", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "", "", 1, 4*time.Second, 0)
	})
	t.Run("reminder with dueTime repeats 3 times", func(t *testing.T) {
		reminderRepeats(ctx, t, "2s", "R3/PT2S", "", 3, 10*time.Second, 0)
	})
	t.Run("reminder without dueTime repeats 3 times", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "R3/PT2S", "", 3, 8*time.Second, 0)
	})
	t.Run("reminder with dueTime deleted after 1 sec", func(t *testing.T) {
		reminderRepeats(ctx, t, time.Now().Add(2*time.Second).Format(time.RFC3339), "PT2S", "", 1, 6*time.Second, 3*time.Second)
	})
	t.Run("reminder without dueTime deleted after 1 sec", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "PT2S", "", 1, 4*time.Second, time.Second)
	})
	t.Run("reminder with dueTime ttl", func(t *testing.T) {
		reminderRepeats(ctx, t, time.Now().Add(2*time.Second).Format(time.RFC3339), "PT2S", "3s", 2, 8*time.Second, 0)
	})
	t.Run("reminder without dueTime ttl", func(t *testing.T) {
		reminderRepeats(ctx, t, "", "2s", time.Now().Add(3*time.Second).Format(time.RFC3339), 2, 6*time.Second, 0)
	})
}

func reminderTTL(ctx context.Context, t *testing.T, dueTime, period, ttl string, repeats int, timeout time.Duration) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	reminder := createReminderData(actorID, actorType, "reminder1", period, dueTime, ttl, "data")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.NoError(t, err)

	var cnt int
	tm := time.NewTimer(timeout)
	defer func() {
		tm.Stop()
	}()
L:
	for {
		select {
		case request := <-requestC:
			assert.Equal(t, reminder.Data, request.Data)
			cnt++
			if cnt > repeats {
				break L
			}
		case <-tm.C:
			break L
		}
	}
	assert.Equal(t, repeats, cnt)
}

func TestReminderTTL(t *testing.T) {
	ctx := context.Background()
	t.Run("reminder ttl with dueTime", func(t *testing.T) {
		reminderTTL(ctx, t, "2s", "R5/PT2S", "5s", 3, 10*time.Second)
	})
	t.Run("reminder ttl without dueTime", func(t *testing.T) {
		reminderTTL(ctx, t, "", "R5/PT2S", "5s", 3, 8*time.Second)
	})
	t.Run("reminder ttl in ISO 8601 with dueTime", func(t *testing.T) {
		reminderTTL(ctx, t, "2s", "R5/PT2S", "PT5S", 3, 10*time.Second)
	})
	t.Run("reminder ttl in RFC3339 without dueTime", func(t *testing.T) {
		reminderTTL(ctx, t, "", "R5/PT2S", time.Now().Add(5*time.Second).Format(time.RFC3339), 3, 8*time.Second)
	})
	t.Run("reminder ttl expired with dueTime", func(t *testing.T) {
		reminderTTL(ctx, t, "2s", "R5/PT2S", "1s", 1, 4*time.Second)
	})
}

func reminderValidation(ctx context.Context, t *testing.T, dueTime, period, ttl, msg string) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	reminder := createReminderData(actorID, actorType, "reminder4", period, dueTime, ttl, "data")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	if len(msg) != 0 {
		assert.EqualError(t, err, msg)
	} else {
		assert.Error(t, err)
	}
}

func TestReminderValidation(t *testing.T) {
	ctx := context.Background()
	t.Run("reminder dueTime invalid (1)", func(t *testing.T) {
		reminderValidation(ctx, t, "invalid", "R5/PT2S", "1h", "error parsing reminder due time: unsupported time/duration format \"invalid\"")
	})
	t.Run("reminder dueTime invalid (2)", func(t *testing.T) {
		reminderValidation(ctx, t, "R5/PT2S", "R5/PT2S", "1h", "error parsing reminder due time: repetitions are not allowed")
	})
	t.Run("reminder period invalid", func(t *testing.T) {
		reminderValidation(ctx, t, time.Now().Add(time.Minute).Format(time.RFC3339), "invalid", "1h", "error parsing reminder period: unsupported duration format \"invalid\"")
	})
	t.Run("reminder ttl invalid (1)", func(t *testing.T) {
		reminderValidation(ctx, t, "", "", "invalid", "error parsing reminder TTL: unsupported time/duration format \"invalid\"")
	})
	t.Run("reminder ttl invalid (2)", func(t *testing.T) {
		reminderValidation(ctx, t, "", "", "R5/PT2S", "error parsing reminder TTL: repetitions are not allowed")
	})
	t.Run("reminder ttl expired (1)", func(t *testing.T) {
		reminderValidation(ctx, t, "2s", "", "-2s", "")
	})
	t.Run("reminder ttl expired (2)", func(t *testing.T) {
		reminderValidation(ctx, t, "", "", "-2s", "")
	})
	t.Run("reminder ttl expired (3)", func(t *testing.T) {
		now := time.Now().Truncate(time.Second).UTC()
		due := now.Add(2 * time.Second).Format(time.RFC3339)
		ttl := now.Add(time.Second).Format(time.RFC3339)
		reminderValidation(ctx, t, due, "", ttl, "")
	})
	t.Run("reminder ttl expired (4)", func(t *testing.T) {
		now := time.Now().Truncate(time.Second).UTC()
		ttl := now.Add(-1 * time.Second).Format(time.RFC3339)
		reminderValidation(ctx, t, "", "", ttl, "")
	})
}

func TestGetReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
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
	actorKey := constructCompositeKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	timer := createTimerData(actorID, actorType, "timer1", "100ms", "100ms", "", "callback", "")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	assert.Nil(t, err)

	timerKey := constructCompositeKey(actorKey, timer.Name)

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

		timer := createTimerData(actorID, actorType, timerName, "10s", "1s", "0s", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.Nil(t, err)

		timer2 := createTimerData(actorID, actorType, timerName, "PT9S", "PT1S", "PT0S", "callback2", "b")
		testActorsRuntime.CreateTimer(ctx, &timer2)

		timer3 := createTimerData(actorID, actorType, timerName, "8s", "2s", "", "callback3", "c")
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

		timer := createTimerData(actorID, actorType, timerName, "10s", "3s", "", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.Nil(t, err)

		time.Sleep(50 * time.Millisecond)

		timer2 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback2", "b")
		timer3 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback3", "c")
		go testActorsRuntime.CreateTimer(ctx, &timer2)
		go testActorsRuntime.CreateTimer(ctx, &timer3)

		time.Sleep(2 * time.Second)

		timer4 := createTimerData(actorID, actorType, timerName, "7s", "2s", "", "callback4", "d")
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

func timerRepeats(ctx context.Context, t *testing.T, dueTime, period, ttl string, repeats int, timeout, del time.Duration) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	timer := createTimerData(actorID, actorType, "timer", period, dueTime, ttl, "callback", "data")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	if repeats == 0 {
		assert.EqualError(t, err, "timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has zero repetitions")
		return
	}
	assert.NoError(t, err)

	cnt := 0
	var (
		delTimer  *time.Timer
		delTimerC <-chan time.Time
	)
	exitTimer := time.NewTimer(timeout)
	if del > 0 {
		delTimer = time.NewTimer(del)
		delTimerC = delTimer.C
	}
	defer func() {
		exitTimer.Stop()
		if delTimer != nil {
			delTimer.Stop()
		}
	}()
L:
	for {
		select {
		case request := <-requestC:
			assert.Equal(t, timer.Data, request.Data)
			cnt++
			if cnt > repeats {
				break L
			}
		case <-delTimerC:
			testActorsRuntime.DeleteTimer(ctx, &DeleteTimerRequest{
				Name:      timer.Name,
				ActorID:   timer.ActorID,
				ActorType: timer.ActorType,
			})
		case <-exitTimer.C:
			break L
		}
	}
	assert.Equal(t, repeats, cnt)
}

func TestTimerRepeats(t *testing.T) {
	ctx := context.Background()
	t.Run("timer with dueTime is ignored", func(t *testing.T) {
		timerRepeats(ctx, t, "2s", "R0/PT2S", "", 0, 0, 0)
	})
	t.Run("timer without dueTime is ignored", func(t *testing.T) {
		timerRepeats(ctx, t, "", "R0/PT2S", "", 0, 0, 0)
	})
	t.Run("timer with dueTime repeats once", func(t *testing.T) {
		timerRepeats(ctx, t, "2s", "R1/PT2S", "", 1, 6*time.Second, 0)
	})
	t.Run("timer without dueTime repeats once", func(t *testing.T) {
		timerRepeats(ctx, t, "", "R1/PT2S", "", 1, 4*time.Second, 0)
	})
	t.Run("timer with dueTime period not set", func(t *testing.T) {
		timerRepeats(ctx, t, "2s", "", "", 1, 6*time.Second, 0)
	})
	t.Run("timer without dueTime period not set", func(t *testing.T) {
		timerRepeats(ctx, t, "", "", "", 1, 4*time.Second, 0)
	})
	t.Run("timer with dueTime repeats 3 times", func(t *testing.T) {
		timerRepeats(ctx, t, "2s", "R3/PT2S", "", 3, 10*time.Second, 0)
	})
	t.Run("timer without dueTime repeats 3 times", func(t *testing.T) {
		timerRepeats(ctx, t, "", "R3/PT2S", "", 3, 8*time.Second, 0)
	})
	t.Run("timer with dueTime deleted after 1 sec", func(t *testing.T) {
		timerRepeats(ctx, t, time.Now().Add(2*time.Second).Format(time.RFC3339), "PT4S", "", 1, 8*time.Second, 3*time.Second)
	})
	t.Run("timer without dueTime deleted after 1 sec", func(t *testing.T) {
		timerRepeats(ctx, t, "", "PT2S", "", 1, 4*time.Second, time.Second)
	})
	t.Run("timer with dueTime ttl", func(t *testing.T) {
		timerRepeats(ctx, t, time.Now().Add(2*time.Second).Format(time.RFC3339), "PT2S", "3s", 2, 8*time.Second, 0)
	})
	t.Run("timer without dueTime ttl", func(t *testing.T) {
		timerRepeats(ctx, t, "", "4s", time.Now().Add(6*time.Second).Format(time.RFC3339), 2, 10*time.Second, 0)
	})
}

func timerTTL(ctx context.Context, t *testing.T, iso bool) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	ttl := "7s"
	if iso {
		ttl = "PT7S"
	}
	timer := createTimerData(actorID, actorType, "timer", "R5/PT2S", "2s", ttl, "callback", "data")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	assert.Nil(t, err)

	cnt := 0
	tm := time.NewTimer(10 * time.Second)
	defer func() {
		tm.Stop()
	}()
L:
	for {
		select {
		case request := <-requestC:
			assert.Equal(t, timer.Data, request.Data)
			cnt++
			if cnt > 4 {
				break L
			}
		case <-tm.C:
			break L
		}
	}
	assert.Equal(t, 4, cnt)
}

func TestTimerTTL(t *testing.T) {
	ctx := context.Background()
	t.Run("timer ttl", func(t *testing.T) {
		timerTTL(ctx, t, false)
	})
	t.Run("timer ttl with ISO 8601", func(t *testing.T) {
		timerTTL(ctx, t, true)
	})
}

func timerValidation(ctx context.Context, t *testing.T, dueTime, period, ttl, msg string) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID)

	timer := createTimerData(actorID, actorType, "timer", period, dueTime, ttl, "callback", "data")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	assert.EqualError(t, err, msg)
}

func TestTimerValidation(t *testing.T) {
	ctx := context.Background()
	t.Run("timer dueTime invalid (1)", func(t *testing.T) {
		timerValidation(ctx, t, "invalid", "R5/PT2S", "1h", "error parsing timer due time: unsupported time/duration format \"invalid\"")
	})
	t.Run("timer dueTime invalid (2)", func(t *testing.T) {
		timerValidation(ctx, t, "R5/PT2S", "R5/PT2S", "1h", "error parsing timer due time: repetitions are not allowed")
	})
	t.Run("timer period invalid", func(t *testing.T) {
		timerValidation(ctx, t, time.Now().Add(time.Minute).Format(time.RFC3339), "invalid", "1h", "error parsing timer period: unsupported duration format \"invalid\"")
	})
	t.Run("timer ttl invalid (1)", func(t *testing.T) {
		timerValidation(ctx, t, "", "", "invalid", "error parsing timer TTL: unsupported time/duration format \"invalid\"")
	})
	t.Run("timer ttl invalid (2)", func(t *testing.T) {
		timerValidation(ctx, t, "", "", "R5/PT2S", "error parsing timer TTL: repetitions are not allowed")
	})
	t.Run("timer ttl expired (1)", func(t *testing.T) {
		timerValidation(ctx, t, "2s", "", "-2s", "timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime: 2s TTL: -2s")
	})
	t.Run("timer ttl expired (2)", func(t *testing.T) {
		timerValidation(ctx, t, "", "", "-2s", "timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime:  TTL: -2s")
	})
	t.Run("timer ttl expired (3)", func(t *testing.T) {
		now := time.Now().Truncate(time.Second).UTC()
		due := now.Add(2 * time.Second).Format(time.RFC3339)
		ttl := now.Add(time.Second).Format(time.RFC3339)
		timerValidation(ctx, t, due, "", ttl, fmt.Sprintf("timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime: %s TTL: %s", due, ttl))
	})
	t.Run("timer ttl expired (4)", func(t *testing.T) {
		now := time.Now().Truncate(time.Second).UTC()
		ttl := now.Add(-1 * time.Second).Format(time.RFC3339)
		timerValidation(ctx, t, "", "", ttl, fmt.Sprintf("timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime:  TTL: %s", ttl))
	})
}

func TestReminderFires(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 250)
	actorKey := constructCompositeKey(actorType, actorID)
	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotNil(t, track)
	assert.NotEmpty(t, track.LastFiredTime)
}

func TestReminderDueDate(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "500ms", "", "a")
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
	actorKey := constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 250)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.NotEmpty(t, track.LastFiredTime)

	time.Sleep(time.Second * 3)

	track2, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.NoError(t, err)
	assert.NotEmpty(t, track2.LastFiredTime)

	assert.NotEqual(t, track.LastFiredTime, track2.LastFiredTime)
}

func TestReminderFiresOnceWithEmptyPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "", "100ms", "", "a")
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
		actorKey := constructCompositeKey(testActorType, testActorID)
		act := newActor(testActorType, testActorID, &reentrancyStackDepth)

		// add test actor
		testActorRuntime.actorsTable.LoadOrStore(actorKey, act)
		act.lock(nil)
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
		assert.EqualError(t, err, "operation type Wrong not supported")
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
	assert.False(t, testActorRuntime.appHealthy.Load())
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

	actorKey := constructCompositeKey(appID, actorType, actorID)

	assert.Equal(t, "myapp||TestActor||abc123", actorKey)
}

func TestConfig(t *testing.T) {
	c := NewConfig("localhost:5050", "app1", []string{"placement:5050"}, []string{"1"}, 3500,
		"1s", "2s", "3s", true, "default", config.ReentrancyConfig{}, 0)
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

func TestReentrancyConfig(t *testing.T) {
	t.Run("Test empty reentrancy values", func(t *testing.T) {
		c := NewConfig("localhost:5050", "app1", []string{"placement:5050"}, []string{"1"}, 3500, "1s", "2s", "3s", true, "default",
			config.ReentrancyConfig{}, 0)
		assert.False(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test minimum reentrancy values", func(t *testing.T) {
		c := NewConfig("localhost:5050", "app1", []string{"placement:5050"}, []string{"1"}, 3500, "1s", "2s", "3s", true, "default",
			config.ReentrancyConfig{Enabled: true}, 0)
		assert.True(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test full reentrancy values", func(t *testing.T) {
		reentrancyLimit := 64
		c := NewConfig("localhost:5050", "app1", []string{"placement:5050"}, []string{"1"}, 3500, "1s", "2s", "3s", true, "default",
			config.ReentrancyConfig{Enabled: true, MaxStackDepth: &reentrancyLimit}, 0)
		assert.True(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 64, *c.Reentrancy.MaxStackDepth)
	})
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

func TestParseDuration(t *testing.T) {
	t.Run("parse time.Duration", func(t *testing.T) {
		duration, repetition, err := parseDuration("0h30m0s")
		assert.Nil(t, err)
		assert.Equal(t, time.Minute*30, duration)
		assert.Equal(t, -1, repetition)
	})
	t.Run("parse ISO 8601 duration with repetition", func(t *testing.T) {
		duration, repetition, err := parseDuration("R5/PT30M")
		assert.Nil(t, err)
		assert.Equal(t, time.Minute*30, duration)
		assert.Equal(t, 5, repetition)
	})
	t.Run("parse ISO 8601 duration without repetition", func(t *testing.T) {
		duration, repetition, err := parseDuration("P1MT2H10M3S")
		assert.Nil(t, err)
		assert.Equal(t, time.Hour*24*30+time.Hour*2+time.Minute*10+time.Second*3, duration)
		assert.Equal(t, -1, repetition)
	})
	t.Run("parse RFC3339 datetime", func(t *testing.T) {
		_, _, err := parseDuration(time.Now().Add(time.Minute).Format(time.RFC3339))
		assert.NotNil(t, err)
	})
	t.Run("parse empty string", func(t *testing.T) {
		_, _, err := parseDuration("")
		assert.NotNil(t, err)
	})
}

func TestParseTime(t *testing.T) {
	t.Run("parse time.Duration without offset", func(t *testing.T) {
		expected := time.Now().Add(30 * time.Minute)
		tm, err := parseTime("0h30m0s", nil)
		assert.NoError(t, err)
		assert.LessOrEqual(t, tm.Sub(expected), time.Second*2)
	})
	t.Run("parse time.Duration with offset", func(t *testing.T) {
		now := time.Now()
		offs := 5 * time.Second
		start := now.Add(offs)
		expected := start.Add(30 * time.Minute)
		tm, err := parseTime("0h30m0s", &start)
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), expected.Sub(tm))
	})
	t.Run("parse ISO 8601 duration with repetition", func(t *testing.T) {
		_, err := parseTime("R5/PT30M", nil)
		assert.Error(t, err)
	})
	t.Run("parse ISO 8601 duration without repetition", func(t *testing.T) {
		now := time.Now()
		offs := 5 * time.Second
		start := now.Add(offs)
		expected := start.Add(time.Hour*24*30 + time.Hour*2 + time.Minute*10 + time.Second*3)
		tm, err := parseTime("P1MT2H10M3S", &start)
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), expected.Sub(tm))
	})
	t.Run("parse RFC3339 datetime", func(t *testing.T) {
		dummy := time.Now().Add(5 * time.Minute)
		expected := time.Now().Truncate(time.Minute).Add(time.Minute)
		tm, err := parseTime(expected.Format(time.RFC3339), &dummy)
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(0), expected.Sub(tm))
	})
	t.Run("parse empty string", func(t *testing.T) {
		_, err := parseTime("", nil)
		assert.EqualError(t, err, "unsupported time/duration format \"\"")
	})
}

func TestBasicReentrantActorLocking(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")
	req2 := invokev1.NewInvokeMethodRequest("second").WithActor("reentrant", "1")

	reentrantConfig := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{Enabled: true}, 0)
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{req2}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel:  reentrantAppChannel,
		config:      &reentrantConfig,
		featureSpec: []config.FeatureSpec{{Name: "Actor.Reentrancy", Enabled: true}},
	}
	testActorRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorRuntime

	resp, err := testActorRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, []string{
		"Entering actors/reentrant/1/method/first", "Entering actors/reentrant/1/method/second",
		"Exiting actors/reentrant/1/method/second", "Exiting actors/reentrant/1/method/first",
	}, reentrantAppChannel.callLog)
}

func TestReentrantActorLockingOverMultipleActors(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")
	req2 := invokev1.NewInvokeMethodRequest("second").WithActor("other", "1")
	req3 := invokev1.NewInvokeMethodRequest("third").WithActor("reentrant", "1")

	reentrantConfig := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{Enabled: true}, 0)
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{req2, req3}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel:  reentrantAppChannel,
		config:      &reentrantConfig,
		featureSpec: []config.FeatureSpec{{Name: "Actor.Reentrancy", Enabled: true}},
	}
	testActorRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorRuntime

	resp, err := testActorRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, []string{
		"Entering actors/reentrant/1/method/first", "Entering actors/other/1/method/second",
		"Entering actors/reentrant/1/method/third", "Exiting actors/reentrant/1/method/third",
		"Exiting actors/other/1/method/second", "Exiting actors/reentrant/1/method/first",
	}, reentrantAppChannel.callLog)
}

func TestReentrancyStackLimit(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")

	stackDepth := 0
	reentrantConfig := NewConfig("", TestAppID, []string{""}, nil, 0, "", "", "", false, "", config.ReentrancyConfig{Enabled: true, MaxStackDepth: &stackDepth}, 0)
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel:  reentrantAppChannel,
		config:      &reentrantConfig,
		featureSpec: []config.FeatureSpec{{Name: "Actor.Reentrancy", Enabled: true}},
	}
	testActorRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorRuntime

	resp, err := testActorRuntime.callLocalActor(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
}
