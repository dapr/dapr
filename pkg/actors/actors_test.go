/*
Copyright 2021 The Dapr Authors
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
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/ptr"
)

const (
	TestAppID                       = "fakeAppID"
	TestKeyName                     = "key0"
	TestActorMetadataPartitionCount = 3
)

var DefaultAppConfig = config.ApplicationConfig{
	Entities:                   []string{"1", "reentrantActor"},
	ActorIdleTimeout:           "1s",
	ActorScanInterval:          "2s",
	DrainOngoingCallTimeout:    "3s",
	DrainRebalancedActors:      true,
	Reentrancy:                 config.ReentrancyConfig{},
	RemindersStoragePartitions: 0,
}

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

// testRequest is the request object that encapsulates the `data` field of a request.
type testRequest struct {
	Data interface{} `json:"data"`
}

type mockAppChannel struct {
	channel.AppChannel
	requestC chan testRequest
}

var testResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  ptr.Of(1),
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
			},
			Timeouts: map[string]string{
				"fast": "100ms",
			},
		},
		Targets: v1alpha1.Targets{
			Actors: map[string]v1alpha1.ActorPolicyNames{
				"failingActorType": {
					Timeout: "fast",
				},
			},
			Components: map[string]v1alpha1.ComponentPolicyNames{
				"failStore": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
			},
		},
	},
}

func (m *mockAppChannel) GetBaseAddress() string {
	return "http://127.0.0.1"
}

func (m *mockAppChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if m.requestC != nil {
		var request testRequest
		err := json.NewDecoder(req.RawData()).Decode(&request)
		if err == nil {
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
	r.callLog = append(r.callLog, "Entering "+req.Message().Method)
	if len(r.nextCall) > 0 {
		nextReq := r.nextCall[0]
		r.nextCall = r.nextCall[1:]

		if val, ok := req.Metadata()["Dapr-Reentrancy-Id"]; ok {
			header := fasthttp.RequestHeader{}
			header.Add("Dapr-Reentrancy-Id", val.Values[0])
			nextReq.AddHeaders(&header)
		}
		resp, err := r.a.callLocalActor(context.Background(), nextReq)
		if err != nil {
			return nil, err
		}
		defer resp.Close()
	}
	r.callLog = append(r.callLog, "Exiting "+req.Message().Method)

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

func (f *fakeStateStore) Delete(ctx context.Context, req *state.DeleteRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, req.Key)

	return nil
}

func (f *fakeStateStore) BulkDelete(ctx context.Context, req []state.DeleteRequest) error {
	return nil
}

func (f *fakeStateStore) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	item := f.items[req.Key]

	if item == nil {
		return &state.GetResponse{Data: nil, ETag: nil}, nil
	}

	return &state.GetResponse{Data: item.data, ETag: item.etag}, nil
}

func (f *fakeStateStore) BulkGet(ctx context.Context, req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	res := []state.BulkGetResponse{}
	for _, oneRequest := range req {
		oneResponse, err := f.Get(ctx, &state.GetRequest{
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

func (f *fakeStateStore) Set(ctx context.Context, req *state.SetRequest) error {
	b, _ := json.Marshal(&req.Value)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = f.newItem(b)

	return nil
}

func (f *fakeStateStore) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func (f *fakeStateStore) BulkSet(ctx context.Context, req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(ctx context.Context, request *state.TransactionalStateRequest) error {
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
	appChannel     channel.AppChannel
	config         *Config
	actorStore     state.Store
	actorStoreName string
	clock          kclock.WithTicker
}

func (b *runtimeBuilder) buildActorRuntime() *actorsRuntime {
	if b.appChannel == nil {
		b.appChannel = new(mockAppChannel)
	}

	if b.config == nil {
		config := NewConfig(ConfigOpts{
			HostAddress:        "",
			AppID:              TestAppID,
			PlacementAddresses: []string{"placement:5050"},
			Port:               0,
			Namespace:          "",
			AppConfig:          config.ApplicationConfig{},
		})
		b.config = &config
	}

	tracingSpec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	storeName := "actorStore"
	if b.actorStore != nil {
		store = b.actorStore
		storeName = b.actorStoreName
	}

	clock := b.clock
	if clock == nil {
		mc := clocktesting.NewFakeClock(startOfTime)
		clock = mc
	}

	a := newActorsWithClock(ActorsOpts{
		StateStore:     store,
		AppChannel:     b.appChannel,
		Config:         *b.config,
		TracingSpec:    tracingSpec,
		Resiliency:     resiliency.FromConfigurations(log, testResiliency),
		StateStoreName: storeName,
	}, clock)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMock(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	config := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})

	clock := clocktesting.NewFakeClock(startOfTime)

	a := newActorsWithClock(ActorsOpts{
		StateStore:     store,
		AppChannel:     appChannel,
		Config:         config,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
	}, clock)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMockWithoutPlacement(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	config := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{""},
		AppConfig:          config.ApplicationConfig{},
	})

	clock := clocktesting.NewFakeClock(startOfTime)

	a := newActorsWithClock(ActorsOpts{
		AppChannel:     appChannel,
		Config:         config,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
	}, clock)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMockAndNoStore(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	var store state.Store
	config := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{""},
		AppConfig:          config.ApplicationConfig{},
	})

	clock := clocktesting.NewFakeClock(startOfTime)

	a := newActorsWithClock(ActorsOpts{
		StateStore:     store,
		AppChannel:     appChannel,
		Config:         config,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
	}, clock)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel channel.AppChannel) *actorsRuntime {
	spec := config.TracingSpec{SamplingRate: "1"}
	store := fakeStore()
	appConfig := config.ApplicationConfig{
		Entities:                   []string{"cat", "actor2"},
		RemindersStoragePartitions: TestActorMetadataPartitionCount,
		EntityConfigs: []config.EntityConfig{
			{
				Entities:                   []string{"actor2"},
				RemindersStoragePartitions: 20,
			},
		},
	}
	c := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          appConfig,
	})

	clock := clocktesting.NewFakeClock(startOfTime)

	a := newActorsWithClock(ActorsOpts{
		StateStore:     store,
		AppChannel:     appChannel,
		Config:         c,
		TracingSpec:    spec,
		Resiliency:     resiliency.New(log),
		StateStoreName: "actorStore",
	}, clock)

	return a.(*actorsRuntime)
}

func newTestActorsRuntimeWithoutStore() *actorsRuntime {
	appChannel := new(mockAppChannel)

	return newTestActorsRuntimeWithMockAndNoStore(appChannel)
}

func newTestActorsRuntime() *actorsRuntime {
	appChannel := new(mockAppChannel)

	return newTestActorsRuntimeWithMock(appChannel)
}

func newTestActorsRuntimeWithoutPlacement() *actorsRuntime {
	appChannel := new(mockAppChannel)

	return newTestActorsRuntimeWithMockWithoutPlacement(appChannel)
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

func fakeCallAndActivateActor(actors *actorsRuntime, actorType, actorID string, clock kclock.WithTicker) {
	actorKey := constructCompositeKey(actorType, actorID)
	actors.actorsTable.LoadOrStore(actorKey, newActor(actorType, actorID, &reentrancyStackDepth, clock))
}

func deactivateActorWithDuration(testActorsRuntime *actorsRuntime, actorType, actorID string) <-chan struct{} {
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	ch := make(chan struct{}, 1)
	go testActorsRuntime.deactivationTicker(testActorsRuntime.config, func(at, aid string) error {
		if actorType == at {
			testActorsRuntime.removeActorFromTable(at, aid)
			ch <- struct{}{}
		}
		return nil
	})
	return ch
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

func assertTestSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan struct{}) {
	t.Helper()

	end := clock.Now().Add(700 * time.Millisecond)

	for {
		select {
		case <-ch:
			// all good
			return
		default:
		}

		if clock.Now().After(end) {
			require.Fail(t, "did not receive signal in 700ms")
		}

		// The signal is sent in a background goroutine, so we need to use a wall
		// clock here
		time.Sleep(time.Millisecond * 5)
		advanceTickers(t, clock, time.Millisecond*10)
	}
}

func assertNoTestSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	// The signal is sent in a background goroutine, so we need to use a wall clock here
	select {
	case <-ch:
		t.Fatalf("received unexpected signal")
	case <-time.After(500 * time.Millisecond):
		// all good
	}
}

// Makes tickers advance
func advanceTickers(t *testing.T, clock *clocktesting.FakeClock, step time.Duration) {
	t.Helper()

	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, time.Second, time.Millisecond, "ticker in program not created in time")
	clock.Step(step)
}

func TestDeactivationTicker(t *testing.T) {
	t.Run("actor is deactivated", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		actorKey := constructCompositeKey(actorType, actorID)

		testActorsRuntime.config.ActorIdleTimeout = time.Second * 2
		testActorsRuntime.config.ActorDeactivationScanInterval = time.Second * 1

		ch := deactivateActorWithDuration(testActorsRuntime, actorType, actorID)

		_, exists := testActorsRuntime.actorsTable.Load(actorKey)
		assert.True(t, exists)

		advanceTickers(t, clock, time.Second*3)

		assertTestSignal(t, clock, ch)

		_, exists = testActorsRuntime.actorsTable.Load(actorKey)
		assert.False(t, exists)
	})

	t.Run("actor is not deactivated", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		actorKey := constructCompositeKey(actorType, actorID)

		testActorsRuntime.config.ActorIdleTimeout = time.Second * 5
		testActorsRuntime.config.ActorDeactivationScanInterval = time.Second * 1

		ch := deactivateActorWithDuration(testActorsRuntime, actorType, actorID)

		_, exists := testActorsRuntime.actorsTable.Load(actorKey)
		assert.True(t, exists)

		advanceTickers(t, clock, time.Second*3)
		assertNoTestSignal(t, ch)

		_, exists = testActorsRuntime.actorsTable.Load(actorKey)
		assert.True(t, exists)
	})

	t.Run("per-actor timeout", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		firstType := "a"
		secondType := "b"
		actorID := "1"

		testActorsRuntime.config.EntityConfigs[firstType] = EntityConfig{Entities: []string{firstType}, ActorIdleTimeout: time.Second * 2}
		testActorsRuntime.config.EntityConfigs[secondType] = EntityConfig{Entities: []string{secondType}, ActorIdleTimeout: time.Second * 5}
		testActorsRuntime.config.ActorDeactivationScanInterval = time.Second * 1

		ch1 := deactivateActorWithDuration(testActorsRuntime, firstType, actorID)
		ch2 := deactivateActorWithDuration(testActorsRuntime, secondType, actorID)

		advanceTickers(t, clock, time.Second*2)
		assertTestSignal(t, clock, ch1)
		assertNoTestSignal(t, ch2)

		_, exists := testActorsRuntime.actorsTable.Load(constructCompositeKey(firstType, actorID))
		assert.False(t, exists)

		_, exists = testActorsRuntime.actorsTable.Load(constructCompositeKey(secondType, actorID))
		assert.True(t, exists)
	})
}

func TestStoreIsNotInitialized(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	testActorsRuntime.store = nil

	t.Run("getReminderTrack", func(t *testing.T) {
		r, e := testActorsRuntime.getReminderTrack("foo", "bar")
		assert.NotNil(t, e)
		assert.Nil(t, r)
	})

	t.Run("updateReminderTrack", func(t *testing.T) {
		e := testActorsRuntime.updateReminderTrack("foo", "bar", 1, testActorsRuntime.clock.Now(), nil)
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

	t.Run("RenameReminder", func(t *testing.T) {
		e := testActorsRuntime.RenameReminder(context.Background(), &RenameReminderRequest{})
		assert.NotNil(t, e)
	})
}

func TestTimerExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	err := testActorsRuntime.executeTimer(actorType, actorID, "timer1", "2s", "2s", "callback", "data")
	assert.NoError(t, err)
}

func TestTimerExecutionZeroDuration(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	err := testActorsRuntime.executeTimer(actorType, actorID, "timer1", "0ms", "0ms", "callback", "data")
	assert.NoError(t, err)
}

func TestReminderExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)
	reminder := &Reminder{
		ActorType: actorType,
		ActorID:   actorID,
		DueTime:   "2s",
		Period:    "2s",
		Name:      "reminder1",
		Data:      "data",
	}
	err := testActorsRuntime.executeReminder(reminder)
	assert.NoError(t, err)
}

func TestReminderExecutionZeroDuration(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)
	reminder := &Reminder{
		ActorType: actorType,
		ActorID:   actorID,
		DueTime:   "0ms",
		Period:    "0ms",
		Name:      "reminder0",
		Data:      "data",
	}
	err := testActorsRuntime.executeReminder(reminder)
	assert.NoError(t, err)
}

func TestSetReminderTrack(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	noRepetition := -1
	err := testActorsRuntime.updateReminderTrack(actorType, actorID, noRepetition, testActorsRuntime.clock.Now(), nil)
	assert.NoError(t, err)
}

func TestGetReminderTrack(t *testing.T) {
	t.Run("reminder doesn't exist", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.Empty(t, r.LastFiredTime)
	})

	t.Run("reminder exists", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		repetition := 10
		now := testActorsRuntime.clock.Now()
		testActorsRuntime.updateReminderTrack(actorType, actorID, repetition, now, nil)
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
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	secondActorType := "actor2"
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
	assert.NoError(t, err)

	err = testActorsRuntime.CreateReminder(ctx, &CreateReminderRequest{
		ActorID:   actorID,
		ActorType: secondActorType,
		Name:      "reminder0",
		Period:    "1s",
		DueTime:   "1s",
		TTL:       "PT10M",
		Data:      nil,
	})
	assert.NoError(t, err)

	// Now creates new reminders and migrates the previous one.
	testActorsRuntimeWithPartition := newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel)
	defer testActorsRuntimeWithPartition.Stop()

	testActorsRuntimeWithPartition.store = testActorsRuntime.store
	testActorsRuntimeWithPartition.transactionalStore = testActorsRuntime.transactionalStore
	for i := 1; i < numReminders; i++ {
		for _, reminderActorType := range []string{actorType, secondActorType} {
			err = testActorsRuntimeWithPartition.CreateReminder(ctx, &CreateReminderRequest{
				ActorID:   actorID,
				ActorType: reminderActorType,
				Name:      "reminder" + strconv.Itoa(i),
				Period:    "1s",
				DueTime:   "1s",
				TTL:       "10m",
				Data:      nil,
			})
			assert.NoError(t, err)
		}
	}

	// Does not migrate yet
	_, actorTypeMetadata, err := testActorsRuntimeWithPartition.getRemindersForActorType(actorType, false)
	assert.NoError(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Check for 2nd type.
	_, actorTypeMetadata, err = testActorsRuntimeWithPartition.getRemindersForActorType(secondActorType, false)
	assert.NoError(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Migrates here.
	reminderReferences, actorTypeMetadata, err := testActorsRuntimeWithPartition.getRemindersForActorType(actorType, true)
	assert.NoError(t, err)
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

	// Check for 2nd type.
	secondReminderReferences, secondTypeMetadata, err := testActorsRuntimeWithPartition.getRemindersForActorType(secondActorType, true)
	assert.NoError(t, err)
	assert.True(t, len(secondTypeMetadata.ID) > 0)
	assert.Equal(t, 20, secondTypeMetadata.RemindersMetadata.PartitionCount)

	partitions = map[uint32]bool{}
	reminders = map[string]bool{}
	for _, reminderRef := range secondReminderReferences {
		partition := reminderRef.actorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.reminder.Name] = true
		assert.Equal(t, secondTypeMetadata.ID, reminderRef.actorMetadataID)
	}
	assert.Equal(t, 20, len(partitions))
	assert.Equal(t, numReminders, len(secondReminderReferences))
	assert.Equal(t, numReminders, len(reminders))
}

func TestRenameReminder(t *testing.T) {
	appChannel := new(mockAppChannel)
	testActorsRuntime := newTestActorsRuntimeWithMock(appChannel)
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	err := testActorsRuntime.CreateReminder(ctx, &CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder0",
		Period:    "1s",
		DueTime:   "1s",
		TTL:       "PT10M",
		Data:      "a",
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))

	// rename reminder
	err = testActorsRuntime.RenameReminder(ctx, &RenameReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		OldName:   "reminder0",
		NewName:   "reminder1",
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))

	// verify that the reminder retrieved with the old name no longer exists
	oldReminder, err := testActorsRuntime.GetReminder(ctx, &GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      "reminder0",
	})
	assert.NoError(t, err)
	assert.Nil(t, oldReminder)

	// verify that the reminder retrieved with the new name already exists
	newReminder, err := testActorsRuntime.GetReminder(ctx, &GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      "reminder1",
	})
	assert.NoError(t, err)
	assert.NotNil(t, newReminder)
	assert.Equal(t, "1s", newReminder.Period)
	assert.Equal(t, "1s", newReminder.DueTime)
	assert.Equal(t, "a", newReminder.Data)
}

func TestOverrideReminder(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		assert.Equal(t, "b", reminders[0].reminder.Data)
	})

	t.Run("override dueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "1s", "2s", "", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		assert.Equal(t, "2s", reminders[0].reminder.DueTime)
	})

	t.Run("override period", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		reminder2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "", "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		assert.Equal(t, "2s", reminders[0].reminder.Period)
	})

	t.Run("override TTL", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "PT5M", "")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		ttl := "9999-09-01T00:00:00Z"
		origTime, err := time.Parse(time.RFC3339, ttl)
		assert.NoError(t, err)
		reminder2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", ttl, "")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		require.NotEmpty(t, reminders)
		newTime, err := time.Parse(time.RFC3339, reminders[0].reminder.ExpirationTime)
		assert.NoError(t, err)
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
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "1s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		reminder2 := createReminderData(actorID, actorType, reminderName, "9s", "1s", "", "b")
		testActorsRuntime.CreateReminder(ctx, &reminder2)
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "9s", reminders[0].reminder.Period)
		assert.Equal(t, "1s", reminders[0].reminder.DueTime)
		assert.Equal(t, "b", reminders[0].reminder.Data)

		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "2s", "", "c")
		testActorsRuntime.CreateReminder(ctx, &reminder3)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "8s", reminders[0].reminder.Period)
		assert.Equal(t, "2s", reminders[0].reminder.DueTime)
		assert.Equal(t, "c", reminders[0].reminder.Data)

		// due time for reminder3 is 2s
		advanceTickers(t, clock, time.Second*2)

		// The reminder update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].reminder.Data, request.Data)
		case <-time.After(1500 * time.Millisecond):
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
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		reminder := createReminderData(actorID, actorType, reminderName, "10s", "3s", "", "a")
		err := testActorsRuntime.CreateReminder(ctx, &reminder)
		assert.NoError(t, err)

		reminder2 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "b")
		reminder3 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "c")
		require.NoError(t, testActorsRuntime.CreateReminder(ctx, &reminder2))
		require.NoError(t, testActorsRuntime.CreateReminder(ctx, &reminder3))

		// due time for reminders is 4s, advance less
		advanceTickers(t, clock, time.Second*2)

		// Check reminder is updated
		reminders, _, err := testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)
		// The statestore could have either reminder2 or reminder3 based on the timing.
		// Therefore, not verifying data field
		assert.Equal(t, "8s", reminders[0].reminder.Period)
		assert.Equal(t, "4s", reminders[0].reminder.DueTime)

		reminder4 := createReminderData(actorID, actorType, reminderName, "7s", "2s", "", "d")
		testActorsRuntime.CreateReminder(ctx, &reminder4)
		reminders, _, err = testActorsRuntime.getRemindersForActorType(actorType, false)
		assert.NoError(t, err)

		// due time for reminder is 2s
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		// The reminder update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, reminders[0].reminder.Data, request.Data)

			// Check reminder is updated
			assert.Equal(t, "7s", reminders[0].reminder.Period)
			assert.Equal(t, "2s", reminders[0].reminder.DueTime)
			assert.Equal(t, "d", reminders[0].reminder.Data)
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestDeleteReminder(t *testing.T) {
	appChannel := new(mockAppChannel)
	testActorsRuntime := newTestActorsRuntimeWithMockAndActorMetadataPartition(appChannel)
	defer testActorsRuntime.Stop()

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
	assert.NoError(t, err)
	assert.Equal(t, 0, len(testActorsRuntime.reminders[actorType]))
}

func TestDeleteReminderWithPartitions(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

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
	assert.NoError(t, err)
	assert.Equal(t, 0, len(testActorsRuntime.reminders[actorType]))
}

func Test_ReminderRepeats(t *testing.T) {
	tests := map[string]struct {
		dueTimeAny      any
		period          string
		ttlAny          any
		delAfterSeconds float64
		expRepeats      int
	}{
		"reminder with dueTime is ignored": {
			dueTimeAny:      "2s",
			period:          "R0/PT2S",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      0,
		},
		"reminder without dueTime is ignored": {
			dueTimeAny:      "",
			period:          "R0/PT2S",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      0,
		},
		"reminder with dueTime repeats once": {
			dueTimeAny:      "2s",
			period:          "R1/PT2S",
			ttlAny:          "",
			delAfterSeconds: 6,
			expRepeats:      1,
		},
		"reminder without dueTime repeats once": {
			dueTimeAny:      "",
			period:          "R1/PT2S",
			ttlAny:          "",
			delAfterSeconds: 4,
			expRepeats:      1,
		},
		"reminder with dueTime repeats no set": {
			dueTimeAny:      "2s",
			period:          "",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      1,
		},
		"reminder with dueTime repeats not set": {
			dueTimeAny:      "2s",
			period:          "",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      1,
		},
		"reminder without dueTime repeats not set": {
			dueTimeAny:      "",
			period:          "",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      1,
		},
		"reminder with dueTime repeats 3 times": {
			dueTimeAny:      "2s",
			period:          "R3/PT2S",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      3,
		},
		"reminder without dueTime repeats 3 times": {
			dueTimeAny:      "",
			period:          "R3/PT2S",
			ttlAny:          "",
			delAfterSeconds: 0,
			expRepeats:      3,
		},
		"reminder with dueTime deleted after 1 sec": {
			dueTimeAny:      2,
			period:          "PT2S",
			ttlAny:          "",
			delAfterSeconds: 3,
			expRepeats:      1,
		},
		"reminder without dueTime deleted after 1 sec": {
			dueTimeAny:      "",
			period:          "PT2S",
			ttlAny:          "",
			delAfterSeconds: 1,
			expRepeats:      1,
		},
		"reminder with dueTime ttl": {
			dueTimeAny:      2,
			period:          "PT2S",
			ttlAny:          "3s",
			delAfterSeconds: 0,
			expRepeats:      2,
		},
		"reminder without dueTime ttl": {
			dueTimeAny:      "",
			period:          "2s",
			ttlAny:          3,
			delAfterSeconds: 0,
			expRepeats:      2,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest, 10)
			appChannel := mockAppChannel{
				requestC: requestC,
			}
			testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
			t.Cleanup(testActorsRuntime.Stop)
			clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, clock)

			var dueTime string
			switch x := test.dueTimeAny.(type) {
			case string:
				dueTime = x
			case int:
				dueTime = clock.Now().Add(time.Duration(x) * time.Second).Format(time.RFC3339)
			}

			var ttl string
			switch x := test.ttlAny.(type) {
			case string:
				ttl = x
			case int:
				ttl = clock.Now().Add(time.Duration(x) * time.Second).Format(time.RFC3339)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)

			reminder := CreateReminderRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "reminder1",
				Period:    test.period,
				DueTime:   dueTime,
				TTL:       ttl,
				Data:      "data",
			}

			err := testActorsRuntime.CreateReminder(ctx, &reminder)
			if test.expRepeats == 0 {
				assert.EqualError(t, err, "reminder reminder1 has zero repetitions")
				return
			}
			assert.NoError(t, err)

			testActorsRuntime.remindersLock.RLock()
			assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))
			testActorsRuntime.remindersLock.RUnlock()

			count := 0

			// Ensure ticker is setup.
			advanceTickers(t, clock, 0)

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				start := clock.Now()

				ticker := clock.NewTicker(time.Second)
				defer ticker.Stop()

				for i := 0; i < 10; i++ {
					if test.delAfterSeconds > 0 && clock.Now().Sub(start).Seconds() >= test.delAfterSeconds {
						require.NoError(t, testActorsRuntime.DeleteReminder(ctx, &DeleteReminderRequest{
							Name:      reminder.Name,
							ActorID:   reminder.ActorID,
							ActorType: reminder.ActorType,
						}))
					}
					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, reminder.Data, request.Data)
						count++
					case <-ticker.C():
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					require.Equal(t, test.expRepeats, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func Test_ReminderTTL(t *testing.T) {
	tests := map[string]struct {
		dueTime    string
		period     string
		ttlAny     any
		expRepeats int
	}{
		"reminder ttl with dueTime": {
			dueTime:    "2s",
			period:     "R5/PT2S",
			ttlAny:     "5s",
			expRepeats: 3,
		},
		"reminder ttl without dueTime": {
			dueTime:    "",
			period:     "R5/PT2S",
			ttlAny:     "5s",
			expRepeats: 3,
		},
		"reminder ttl in ISO 8601 with dueTime": {
			dueTime:    "2s",
			period:     "R5/PT2S",
			ttlAny:     "PT5S",
			expRepeats: 3,
		},
		"reminder ttl in RFC3339 without dueTime": {
			dueTime:    "",
			period:     "R5/PT2S",
			ttlAny:     5,
			expRepeats: 3,
		},
		"reminder ttl expired with dueTime": {
			dueTime:    "2s",
			period:     "R5/PT2S",
			ttlAny:     "1s",
			expRepeats: 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest)
			appChannel := mockAppChannel{
				requestC: requestC,
			}
			testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
			t.Cleanup(testActorsRuntime.Stop)
			clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, clock)

			var ttl string
			switch x := test.ttlAny.(type) {
			case string:
				ttl = x
			case int:
				ttl = clock.Now().Add(time.Duration(x) * time.Second).Format(time.RFC3339)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)

			reminder := CreateReminderRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "reminder1",
				Period:    test.period,
				DueTime:   test.dueTime,
				TTL:       ttl,
				Data:      "data",
			}
			err := testActorsRuntime.CreateReminder(ctx, &reminder)
			assert.NoError(t, err)

			count := 0

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				ticker := clock.NewTicker(time.Second)
				defer ticker.Stop()

				for i := 0; i < 10; i++ {
					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, reminder.Data, request.Data)
						count++
					case <-ctx.Done():
					case <-ticker.C():
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					require.Equal(t, test.expRepeats, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func reminderValidation(ctx context.Context, t *testing.T, dueTime, period, ttl, msg string) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

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
		due := startOfTime.Add(2 * time.Second).Format(time.RFC3339)
		ttl := startOfTime.Add(time.Second).Format(time.RFC3339)
		reminderValidation(ctx, t, due, "", ttl, "")
	})
	t.Run("reminder ttl expired (4)", func(t *testing.T) {
		ttl := startOfTime.Add(-1 * time.Second).Format(time.RFC3339)
		reminderValidation(ctx, t, "", "", ttl, "")
	})
}

func TestGetReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

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
	assert.NoError(t, err)
	assert.Equal(t, r.Data, "a")
	assert.Equal(t, r.Period, "1s")
	assert.Equal(t, r.DueTime, "1s")
}

func TestCreateTimerDueTimes(t *testing.T) {
	t.Run("test create timer with positive DueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		actorType, actorID := getTestActorTypeAndID()
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		timer := createTimerData(actorID, actorType, "positiveTimer", "1s", "2s", "", "callback", "testTimer")
		err := testActorsRuntime.CreateTimer(context.Background(), &timer)
		assert.NoError(t, err)
	})

	t.Run("test create timer with 0 DueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		actorType, actorID := getTestActorTypeAndID()
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		timer := createTimerData(actorID, actorType, "positiveTimer", "1s", "0s", "", "callback", "testTimer")
		err := testActorsRuntime.CreateTimer(context.Background(), &timer)
		assert.NoError(t, err)
	})

	t.Run("test create timer with no DueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()
		actorType, actorID := getTestActorTypeAndID()
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		timer := createTimerData(actorID, actorType, "positiveTimer", "1s", "", "", "callback", "testTimer")
		err := testActorsRuntime.CreateTimer(context.Background(), &timer)
		assert.NoError(t, err)
	})
}

func TestDeleteTimer(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	timer := createTimerData(actorID, actorType, "timer1", "100ms", "100ms", "", "callback", "")
	err := testActorsRuntime.CreateTimer(ctx, &timer)
	assert.NoError(t, err)

	timerKey := constructCompositeKey(actorKey, timer.Name)

	_, ok := testActorsRuntime.activeTimers.Load(timerKey)
	assert.True(t, ok)

	err = testActorsRuntime.DeleteTimer(ctx, &DeleteTimerRequest{
		Name:      timer.Name,
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.NoError(t, err)

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
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)
		timerName := "timer1"

		timer := createTimerData(actorID, actorType, timerName, "10s", "1s", "0s", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.NoError(t, err)

		timer2 := createTimerData(actorID, actorType, timerName, "PT9S", "PT1S", "PT0S", "callback2", "b")
		testActorsRuntime.CreateTimer(ctx, &timer2)

		timer3 := createTimerData(actorID, actorType, timerName, "8s", "2s", "", "callback3", "c")
		testActorsRuntime.CreateTimer(ctx, &timer3)

		// due time for timer3 is 2s
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		// The timer update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, timer3.Data, request.Data)
		case <-time.After(1500 * time.Millisecond):
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
		defer testActorsRuntime.Stop()
		clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		timerName := "timer1"
		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		timer := createTimerData(actorID, actorType, timerName, "10s", "3s", "", "callback1", "a")
		err := testActorsRuntime.CreateTimer(ctx, &timer)
		assert.NoError(t, err)

		timer2 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback2", "b")
		timer3 := createTimerData(actorID, actorType, timerName, "8s", "4s", "", "callback3", "c")
		require.NoError(t, testActorsRuntime.CreateTimer(ctx, &timer2))
		require.NoError(t, testActorsRuntime.CreateTimer(ctx, &timer3))

		// due time for timer2/timer3 is 4s, advance less
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		timer4 := createTimerData(actorID, actorType, timerName, "7s", "2s", "", "callback4", "d")
		testActorsRuntime.CreateTimer(ctx, &timer4)

		// due time for timer4 is 2s
		advanceTickers(t, clock, time.Second*2)

		// The timer update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, timer4.Data, request.Data)
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func Test_TimerRepeats(t *testing.T) {
	tests := map[string]struct {
		dueTime         string
		period          string
		ttl             string
		expRepeats      int
		delAfterSeconds float64
	}{
		"timer with dueTime is ignored": {
			dueTime:         "2s",
			period:          "R0/PT2S",
			ttl:             "",
			expRepeats:      0,
			delAfterSeconds: 0,
		},
		"timer without dueTime is ignored": {
			dueTime:         "",
			period:          "R0/PT2S",
			ttl:             "",
			expRepeats:      0,
			delAfterSeconds: 0,
		},
		"timer with dueTime repeats once": {
			dueTime:         "2s",
			period:          "R1/PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer without dueTime repeats once": {
			dueTime:         "",
			period:          "R1/PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer with dueTime period not set": {
			dueTime:         "2s",
			period:          "",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer without dueTime period not set": {
			dueTime:         "",
			period:          "",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 0,
		},
		"timer with dueTime repeats 3 times": {
			dueTime:         "2s",
			period:          "R3/PT2S",
			ttl:             "",
			expRepeats:      3,
			delAfterSeconds: 0,
		},
		"timer without dueTime repeats 3 times": {
			dueTime:         "",
			period:          "R3/PT2S",
			ttl:             "",
			expRepeats:      3,
			delAfterSeconds: 0,
		},
		"timer with dueTime deleted after 1 sec": {
			dueTime:         startOfTime.Add(2 * time.Second).Format(time.RFC3339),
			period:          "PT4S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 3,
		},
		"timer without dueTime deleted after 1 sec": {
			dueTime:         "",
			period:          "PT2S",
			ttl:             "",
			expRepeats:      1,
			delAfterSeconds: 1,
		},
		"timer with dueTime ttl": {
			dueTime:         startOfTime.Add(2 * time.Second).Format(time.RFC3339),
			period:          "PT2S",
			ttl:             "3s",
			expRepeats:      2,
			delAfterSeconds: 0,
		},
		"timer without dueTime ttl": {
			dueTime:         "",
			period:          "4s",
			ttl:             startOfTime.Add(6 * time.Second).Format(time.RFC3339),
			expRepeats:      2,
			delAfterSeconds: 0,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest, 10)
			appChannel := mockAppChannel{
				requestC: requestC,
			}
			testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
			defer testActorsRuntime.Stop()
			clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

			timer := CreateTimerRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "timer",
				Period:    test.period,
				DueTime:   test.dueTime,
				TTL:       test.ttl,
				Data:      "data",
				Callback:  "callback",
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)

			err := testActorsRuntime.CreateTimer(ctx, &timer)
			if test.expRepeats == 0 {
				assert.EqualError(t, err, "timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has zero repetitions")
				return
			}
			assert.NoError(t, err)

			count := 0

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				start := clock.Now()
				ticker := clock.NewTicker(time.Second)
				defer ticker.Stop()

				for i := 0; i < 10; i++ {
					if test.delAfterSeconds > 0 && clock.Now().Sub(start).Seconds() >= test.delAfterSeconds {
						require.NoError(t, testActorsRuntime.DeleteTimer(ctx, &DeleteTimerRequest{
							Name:      timer.Name,
							ActorID:   timer.ActorID,
							ActorType: timer.ActorType,
						}))
					}

					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, timer.Data, request.Data)
						count++
					case <-ctx.Done():
					case <-ticker.C():
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					require.Equal(t, test.expRepeats, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func Test_TimerTTL(t *testing.T) {
	tests := map[string]struct {
		iso bool
	}{
		"timer ttl": {
			iso: false,
		},
		"timer ttl with ISO 8601": {
			iso: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			requestC := make(chan testRequest, 10)
			appChannel := mockAppChannel{
				requestC: requestC,
			}
			testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
			defer testActorsRuntime.Stop()
			clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

			ttl := "7s"
			if test.iso {
				ttl = "PT7S"
			}
			timer := createTimerData(actorID, actorType, "timer", "R5/PT2S", "2s", ttl, "callback", "data")

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)
			assert.NoError(t, testActorsRuntime.CreateTimer(ctx, &timer))

			count := 0

			ticker := clock.NewTicker(time.Second)
			defer ticker.Stop()

			advanceTickers(t, clock, 0)

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()

				for i := 0; i < 10; i++ {
					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, timer.Data, request.Data)
						count++
					case <-ticker.C():
						// nop
					}
				}
			}()

			for {
				select {
				case <-ctx.Done():
					assert.Equal(t, 4, count)
					return
				case <-time.After(time.Millisecond):
					advanceTickers(t, clock, time.Millisecond*500)
				}
			}
		})
	}
}

func timerValidation(ctx context.Context, t *testing.T, dueTime, period, ttl, msg string) {
	requestC := make(chan testRequest, 10)
	appChannel := mockAppChannel{
		requestC: requestC,
	}
	testActorsRuntime := newTestActorsRuntimeWithMock(&appChannel)
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

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
		timerValidation(ctx, t, startOfTime.Add(time.Minute).Format(time.RFC3339), "invalid", "1h", "error parsing timer period: unsupported duration format \"invalid\"")
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
		due := startOfTime.Add(2 * time.Second).Format(time.RFC3339)
		ttl := startOfTime.Add(time.Second).Format(time.RFC3339)
		timerValidation(ctx, t, due, "", ttl, fmt.Sprintf("timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime: %s TTL: %s", due, ttl))
	})
	t.Run("timer ttl expired (4)", func(t *testing.T) {
		ttl := startOfTime.Add(-1 * time.Second).Format(time.RFC3339)
		timerValidation(ctx, t, "", "", ttl, fmt.Sprintf("timer cat||e485d5de-de48-45ab-816e-6cc700d18ace||timer has already expired: dueTime:  TTL: %s", ttl))
	})
}

func TestReminderFires(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.NoError(t, err)

	advanceTickers(t, clock, time.Millisecond*101)

	actorKey := constructCompositeKey(actorType, actorID)
	assert.Eventually(t, func() bool {
		track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
		require.NoError(t, err)
		require.NotNil(t, track)
		return len(track.LastFiredTime) > 0
	}, time.Second, time.Millisecond)
}

func TestReminderDueDate(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "100ms", "500ms", "", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.NoError(t, err)

	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.NoError(t, err)
	assert.Empty(t, track.LastFiredTime)

	advanceTickers(t, clock, time.Millisecond*500)

	assert.Eventually(t, func() bool {
		track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
		require.NoError(t, err)
		require.NotNil(t, track)
		return len(track.LastFiredTime) > 0
	}, time.Second, time.Millisecond)
}

func TestReminderPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	require.NoError(t, testActorsRuntime.CreateReminder(ctx, &CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder1",
		Period:    "100ms",
		DueTime:   "100ms",
		TTL:       "",
		Data:      "a",
	}))

	advanceTickers(t, clock, 0)

	var (
		track  *ReminderTrack
		track2 *ReminderTrack
		err    error
	)

	assert.Eventually(t, func() bool {
		track, err = testActorsRuntime.getReminderTrack(actorKey, "reminder1")
		require.NoError(t, err)
		require.NotNil(t, track)
		return len(track.LastFiredTime) > 0
	}, time.Second, time.Millisecond)

	assert.Eventually(t, func() bool {
		advanceTickers(t, clock, time.Millisecond*100)
		track2, err = testActorsRuntime.getReminderTrack(actorKey, "reminder1")
		require.NoError(t, err)
		require.NotNil(t, track2)
		return len(track2.LastFiredTime) > 0 && track.LastFiredTime != track2.LastFiredTime
	}, time.Second, time.Millisecond)
}

func TestReminderFiresOnceWithEmptyPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	reminder := createReminderData(actorID, actorType, "reminder1", "", "100ms", "", "a")
	err := testActorsRuntime.CreateReminder(ctx, &reminder)
	assert.NoError(t, err)

	clock.Step(100 * time.Millisecond)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Empty(t, track.LastFiredTime)
}

func TestConstructActorStateKey(t *testing.T) {
	delim := "||"
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

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
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val any
	json.Unmarshal([]byte(fakeData), &val)

	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
	response, err := testActorsRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	// assert
	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))
}

func TestDeleteState(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	fakeData := strconv.Quote("fakeData")

	var val any
	json.Unmarshal([]byte(fakeData), &val)

	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

	// insert state
	testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
	response, err := testActorsRuntime.GetState(ctx, &GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       TestKeyName,
	})

	// make sure that state is stored.
	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))

	// delete state
	testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
	response, err = testActorsRuntime.GetState(ctx, &GetStateRequest{
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
	defer req.Close()

	t.Run("invoke actor successfully", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		defer resp.Close()
	})

	t.Run("actor is already disposed", func(t *testing.T) {
		// arrange
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorKey := constructCompositeKey(testActorType, testActorID)
		act := newActor(testActorType, testActorID, &reentrancyStackDepth, testActorsRuntime.clock)

		// add test actor
		testActorsRuntime.actorsTable.LoadOrStore(actorKey, act)
		act.lock(nil)
		assert.True(t, act.isBusy())

		// get dispose channel for test actor
		ch := act.channel()
		act.unlock()

		_, closed := <-ch
		assert.False(t, closed, "dispose channel must be closed after unlock")

		// act
		resp, err := testActorsRuntime.callLocalActor(context.Background(), req)

		// assert
		s, _ := status.FromError(err)
		assert.Equal(t, codes.ResourceExhausted, s.Code())
		assert.Nil(t, resp)
	})
}

func TestTransactionalState(t *testing.T) {
	ctx := context.Background()
	t.Run("Single set request succeeds", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		err := testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
		assert.NoError(t, err)
	})

	t.Run("Multiple requests succeeds", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		err := testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
		assert.NoError(t, err)
	})

	t.Run("Wrong request body - should fail", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		err := testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

		err := testActorsRuntime.TransactionalStateOperation(ctx, &TransactionalRequest{
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
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()

	t.Run("create new key", func(t *testing.T) {
		act := testActorsRuntime.getOrCreateActor(testActorType, "id-1")
		assert.NotNil(t, act)
	})

	t.Run("try to create the same key", func(t *testing.T) {
		oldActor := testActorsRuntime.getOrCreateActor(testActorType, "id-2")
		assert.NotNil(t, oldActor)
		newActor := testActorsRuntime.getOrCreateActor(testActorType, "id-2")
		assert.Same(t, oldActor, newActor, "should not create new actor")
	})
}

func TestActiveActorsCount(t *testing.T) {
	ctx := context.Background()
	t.Run("Actors Count", func(t *testing.T) {
		expectedCounts := []ActiveActorsCount{{Type: "cat", Count: 2}, {Type: "dog", Count: 1}}

		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		fakeCallAndActivateActor(testActorsRuntime, "cat", "abcd", testActorsRuntime.clock)
		fakeCallAndActivateActor(testActorsRuntime, "cat", "xyz", testActorsRuntime.clock)
		fakeCallAndActivateActor(testActorsRuntime, "dog", "xyz", testActorsRuntime.clock)

		actualCounts := testActorsRuntime.GetActiveActorsCount(ctx)
		assert.ElementsMatch(t, expectedCounts, actualCounts)
	})

	t.Run("Actors Count empty", func(t *testing.T) {
		expectedCounts := []ActiveActorsCount{}

		testActorsRuntime := newTestActorsRuntime()
		defer testActorsRuntime.Stop()

		actualCounts := testActorsRuntime.GetActiveActorsCount(ctx)
		assert.Equal(t, expectedCounts, actualCounts)
	})
}

func TestActorsAppHealthCheck(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	testActorsRuntime.config.HostedActorTypes = []string{"actor1"}
	go testActorsRuntime.startAppHealthCheck(
		health.WithClock(clock),
		health.WithFailureThreshold(1),
		health.WithInterval(1*time.Second),
		health.WithRequestTimeout(100*time.Millisecond),
	)

	assert.Eventually(t, func() bool {
		advanceTickers(t, clock, time.Second)
		return !testActorsRuntime.appHealthy.Load()
	}, time.Second, time.Microsecond*10, testActorsRuntime.appHealthy.Load())
}

func TestHostedActorsWithoutStateStore(t *testing.T) {
	testActorsRuntime := newTestActorsRuntimeWithoutStore()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	testActorsRuntime.config.HostedActorTypes = []string{"actor1"}
	go testActorsRuntime.startAppHealthCheck(
		health.WithClock(clock),
		health.WithFailureThreshold(1),
		health.WithInterval(1*time.Second),
		health.WithRequestTimeout(100*time.Millisecond),
	)

	assert.Eventually(t, func() bool {
		advanceTickers(t, clock, time.Second)
		return !testActorsRuntime.appHealthy.Load()
	}, time.Second, time.Microsecond*10, testActorsRuntime.appHealthy.Load())
}

func TestNoHostedActorsWithoutStateStore(t *testing.T) {
	testActorsRuntime := newTestActorsRuntimeWithoutStore()
	defer testActorsRuntime.Stop()
	clock := testActorsRuntime.clock.(*clocktesting.FakeClock)

	testActorsRuntime.config.HostedActorTypes = []string{}
	go testActorsRuntime.startAppHealthCheck(
		health.WithClock(clock),
		health.WithFailureThreshold(1),
		health.WithInterval(1*time.Second),
		health.WithRequestTimeout(100*time.Millisecond),
	)

	clock.Step(2 * time.Second)

	assert.Eventually(t, func() bool {
		return testActorsRuntime.appHealthy.Load()
	}, time.Second, time.Microsecond*10)
}

func TestShutdown(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()

	t.Run("no panic when placement is nil", func(t *testing.T) {
		testActorsRuntime.placement = nil
		testActorsRuntime.Stop()
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
	appConfig := config.ApplicationConfig{
		Entities:                   []string{"1"},
		ActorScanInterval:          "1s",
		ActorIdleTimeout:           "2s",
		DrainOngoingCallTimeout:    "3s",
		DrainRebalancedActors:      true,
		Reentrancy:                 config.ReentrancyConfig{},
		RemindersStoragePartitions: 0,
	}
	c := NewConfig(ConfigOpts{
		HostAddress:        "localhost:5050",
		AppID:              "app1",
		PlacementAddresses: []string{"placement:5050"},
		Port:               3500,
		Namespace:          "default",
		AppConfig:          appConfig,
	})
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
		appConfig := DefaultAppConfig
		c := NewConfig(ConfigOpts{
			HostAddress:        "localhost:5050",
			AppID:              "app1",
			PlacementAddresses: []string{"placement:5050"},
			Port:               3500,
			Namespace:          "default",
			AppConfig:          appConfig,
		})
		assert.False(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test per type reentrancy", func(t *testing.T) {
		appConfig := DefaultAppConfig
		appConfig.EntityConfigs = []config.EntityConfig{
			{
				Entities: []string{"reentrantActor"},
				Reentrancy: config.ReentrancyConfig{
					Enabled: true,
				},
			},
		}
		c := NewConfig(ConfigOpts{
			HostAddress:        "localhost:5050",
			AppID:              "app1",
			PlacementAddresses: []string{"placement:5050"},
			Port:               3500,
			Namespace:          "default",
			AppConfig:          appConfig,
		})
		assert.False(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
		assert.True(t, c.EntityConfigs["reentrantActor"].ReentrancyConfig.Enabled)
	})

	t.Run("Test minimum reentrancy values", func(t *testing.T) {
		appConfig := DefaultAppConfig
		appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true}
		c := NewConfig(ConfigOpts{
			HostAddress:        "localhost:5050",
			AppID:              "app1",
			PlacementAddresses: []string{"placement:5050"},
			Port:               3500,
			Namespace:          "default",
			AppConfig:          appConfig,
		})
		assert.True(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test full reentrancy values", func(t *testing.T) {
		appConfig := DefaultAppConfig
		reentrancyLimit := 64
		appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true, MaxStackDepth: &reentrancyLimit}
		c := NewConfig(ConfigOpts{
			HostAddress:        "localhost:5050",
			AppID:              "app1",
			PlacementAddresses: []string{"placement:5050"},
			Port:               3500,
			Namespace:          "default",
			AppConfig:          appConfig,
		})
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

func TestBasicReentrantActorLocking(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")
	defer req.Close()
	req2 := invokev1.NewInvokeMethodRequest("second").WithActor("reentrant", "1")
	defer req2.Close()

	appConfig := DefaultAppConfig
	appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true}
	reentrantConfig := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          appConfig,
	})
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{req2}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel: reentrantAppChannel,
		config:     &reentrantConfig,
	}
	testActorsRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorsRuntime

	resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	defer resp.Close()
	assert.Equal(t, []string{
		"Entering actors/reentrant/1/method/first", "Entering actors/reentrant/1/method/second",
		"Exiting actors/reentrant/1/method/second", "Exiting actors/reentrant/1/method/first",
	}, reentrantAppChannel.callLog)
}

func TestReentrantActorLockingOverMultipleActors(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")
	defer req.Close()
	req2 := invokev1.NewInvokeMethodRequest("second").WithActor("other", "1")
	defer req2.Close()
	req3 := invokev1.NewInvokeMethodRequest("third").WithActor("reentrant", "1")
	defer req3.Close()

	appConfig := DefaultAppConfig
	appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true}
	reentrantConfig := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          appConfig,
	})
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{req2, req3}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel: reentrantAppChannel,
		config:     &reentrantConfig,
	}
	testActorsRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorsRuntime

	resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	defer resp.Close()
	assert.Equal(t, []string{
		"Entering actors/reentrant/1/method/first", "Entering actors/other/1/method/second",
		"Entering actors/reentrant/1/method/third", "Exiting actors/reentrant/1/method/third",
		"Exiting actors/other/1/method/second", "Exiting actors/reentrant/1/method/first",
	}, reentrantAppChannel.callLog)
}

func TestReentrancyStackLimit(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrant", "1")
	defer req.Close()

	stackDepth := 0
	appConfig := DefaultAppConfig
	appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true, MaxStackDepth: &stackDepth}
	reentrantConfig := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          appConfig,
	})
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel: reentrantAppChannel,
		config:     &reentrantConfig,
	}
	testActorsRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorsRuntime

	resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
}

func TestReentrancyPerActor(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrantActor", "1")
	defer req.Close()
	req2 := invokev1.NewInvokeMethodRequest("second").WithActor("reentrantActor", "1")
	defer req2.Close()

	appConfig := DefaultAppConfig
	appConfig.Reentrancy = config.ReentrancyConfig{Enabled: false}
	appConfig.EntityConfigs = []config.EntityConfig{
		{
			Entities: []string{"reentrantActor"},
			Reentrancy: config.ReentrancyConfig{
				Enabled: true,
			},
		},
	}
	reentrantConfig := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{""},
		AppConfig:          appConfig,
	})
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{req2}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel: reentrantAppChannel,
		config:     &reentrantConfig,
	}
	testActorsRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorsRuntime

	resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	defer resp.Close()
	assert.Equal(t, []string{
		"Entering actors/reentrantActor/1/method/first", "Entering actors/reentrantActor/1/method/second",
		"Exiting actors/reentrantActor/1/method/second", "Exiting actors/reentrantActor/1/method/first",
	}, reentrantAppChannel.callLog)
}

func TestReentrancyStackLimitPerActor(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("first").WithActor("reentrantActor", "1")
	defer req.Close()

	stackDepth := 0
	appConfig := DefaultAppConfig
	appConfig.Reentrancy = config.ReentrancyConfig{Enabled: false}
	appConfig.EntityConfigs = []config.EntityConfig{
		{
			Entities: []string{"reentrantActor"},
			Reentrancy: config.ReentrancyConfig{
				Enabled:       true,
				MaxStackDepth: &stackDepth,
			},
		},
	}
	reentrantConfig := NewConfig(ConfigOpts{
		AppID:              TestAppID,
		PlacementAddresses: []string{""},
		AppConfig:          appConfig,
	})
	reentrantAppChannel := new(reentrantAppChannel)
	reentrantAppChannel.nextCall = []*invokev1.InvokeMethodRequest{}
	reentrantAppChannel.callLog = []string{}
	builder := runtimeBuilder{
		appChannel: reentrantAppChannel,
		config:     &reentrantConfig,
	}
	testActorsRuntime := builder.buildActorRuntime()
	reentrantAppChannel.a = testActorsRuntime

	resp, err := testActorsRuntime.callLocalActor(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
}

func TestActorsRuntimeResiliency(t *testing.T) {
	actorType := "failingActor"
	actorID := "failingId"
	failingState := &daprt.FailingStatestore{
		Failure: daprt.NewFailure(
			// Transform the keys into actor format.
			map[string]int{
				constructCompositeKey(TestAppID, actorType, actorID, "failingGetStateKey"): 1,
				constructCompositeKey(TestAppID, actorType, actorID, "failingMultiKey"):    1,
				constructCompositeKey("actors", actorType):                                 1, // Default reminder key.
			},
			map[string]time.Duration{
				constructCompositeKey(TestAppID, actorType, actorID, "timeoutGetStateKey"): time.Second * 10,
				constructCompositeKey(TestAppID, actorType, actorID, "timeoutMultiKey"):    time.Second * 10,
				constructCompositeKey("actors", actorType):                                 time.Second * 10, // Default reminder key.
			},
			map[string]int{},
		),
	}
	failingAppChannel := &daprt.FailingAppChannel{
		Failure: daprt.NewFailure(
			nil,
			map[string]time.Duration{
				"timeoutId": time.Second * 10,
			},
			map[string]int{},
		),
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			return req.Actor().ActorId
		},
	}
	builder := runtimeBuilder{
		appChannel:     failingAppChannel,
		actorStore:     failingState,
		actorStoreName: "failStore",
		// This test is using a real wall clock
		clock: &kclock.RealClock{},
	}
	runtime := builder.buildActorRuntime()

	t.Run("callLocalActor times out with resiliency", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("actorMethod").
			WithActor("failingActorType", "timeoutId").
			WithReplay(true)
		defer req.Close()

		start := time.Now()
		resp, err := runtime.callLocalActor(context.Background(), req)
		end := time.Now()

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, 1, failingAppChannel.Failure.CallCount("timeoutId"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("test get state retries with resiliency", func(t *testing.T) {
		req := &GetStateRequest{
			Key:       "failingGetStateKey",
			ActorType: actorType,
			ActorID:   actorID,
		}
		_, err := runtime.GetState(context.Background(), req)

		callKey := constructCompositeKey(TestAppID, actorType, actorID, "failingGetStateKey")
		assert.NoError(t, err)
		assert.Equal(t, 2, failingState.Failure.CallCount(callKey))
	})

	t.Run("test get state times out with resiliency", func(t *testing.T) {
		req := &GetStateRequest{
			Key:       "timeoutGetStateKey",
			ActorType: actorType,
			ActorID:   actorID,
		}
		start := time.Now()
		_, err := runtime.GetState(context.Background(), req)
		end := time.Now()

		callKey := constructCompositeKey(TestAppID, actorType, actorID, "timeoutGetStateKey")
		assert.Error(t, err)
		assert.Equal(t, 2, failingState.Failure.CallCount(callKey))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("test state transaction retries with resiliency", func(t *testing.T) {
		req := &TransactionalRequest{
			Operations: []TransactionalOperation{
				{
					Operation: Delete,
					Request: map[string]string{
						"key": "failingMultiKey",
					},
				},
			},
			ActorType: actorType,
			ActorID:   actorID,
		}

		err := runtime.TransactionalStateOperation(context.Background(), req)

		callKey := constructCompositeKey(TestAppID, actorType, actorID, "failingMultiKey")
		assert.NoError(t, err)
		assert.Equal(t, 2, failingState.Failure.CallCount(callKey))
	})

	t.Run("test state transaction times out with resiliency", func(t *testing.T) {
		req := &TransactionalRequest{
			Operations: []TransactionalOperation{
				{
					Operation: Delete,
					Request: map[string]string{
						"key": "timeoutMultiKey",
					},
				},
			},
			ActorType: actorType,
			ActorID:   actorID,
		}

		start := time.Now()
		err := runtime.TransactionalStateOperation(context.Background(), req)
		end := time.Now()

		callKey := constructCompositeKey(TestAppID, actorType, actorID, "timeoutMultiKey")
		assert.Error(t, err)
		assert.Equal(t, 2, failingState.Failure.CallCount(callKey))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("test get reminders retries and times out with resiliency", func(t *testing.T) {
		_, err := runtime.GetReminder(context.Background(), &GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
		})

		callKey := constructCompositeKey("actors", actorType)
		assert.NoError(t, err)
		assert.Equal(t, 2, failingState.Failure.CallCount(callKey))

		// Key will no longer fail, so now we can check the timeout.
		start := time.Now()
		_, err = runtime.GetReminder(context.Background(), &GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 4, failingState.Failure.CallCount(callKey)) // Should be called 2 more times.
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

func TestPlacementSwitchIsNotTurnedOn(t *testing.T) {
	testActorsRuntime := newTestActorsRuntimeWithoutPlacement()
	defer testActorsRuntime.Stop()

	t.Run("placement is empty", func(t *testing.T) {
		assert.Nil(t, testActorsRuntime.placement)
	})

	t.Run("the actor store can not be initialized normally", func(t *testing.T) {
		assert.Nil(t, testActorsRuntime.store)
	})
}
