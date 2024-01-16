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

package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	daprt "github.com/dapr/dapr/pkg/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	TestAppID                       = "fakeAppID"
	TestKeyName                     = "key0"
	TestPodName                     = "testPodName"
	TestActorMetadataPartitionCount = 3
	storeName                       = "testStore"
)

func newTestReminders() *reminders {
	conf := internal.Config{
		AppID:            TestAppID,
		ActorsService:    "placement:placement:5050",
		HostedActorTypes: internal.NewHostedActors([]string{"cat"}),
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	apiLevel := &atomic.Uint32{}
	apiLevel.Store(internal.ActorAPILevel)
	r := NewRemindersProvider(internal.ActorsProviderOptions{
		Clock:    clock,
		Config:   conf,
		APILevel: apiLevel,
	})
	store := daprt.NewFakeStateStore()
	r.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return storeName, store, nil
	})
	r.SetLookupActorFn(func(context.Context, string, string) (bool, string) {
		return true, "localhost"
	})
	r.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		return true
	})
	return r.(*reminders)
}

// testRequest is the request object that encapsulates the `data` field of a request.
type testRequest struct {
	Data any `json:"data"`
}

func TestStoreIsNotInitialized(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	testReminders.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return "", nil, errors.New("simulated")
	})

	t.Run("getReminderTrack", func(t *testing.T) {
		r, err := testReminders.getReminderTrack(context.Background(), "foo||bar")
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("updateReminderTrack", func(t *testing.T) {
		err := testReminders.updateReminderTrack(context.Background(), "foo||bar", 1, testReminders.clock.Now(), nil)
		require.Error(t, err)
	})

	t.Run("CreateReminder", func(t *testing.T) {
		req := internal.CreateReminderRequest{}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(context.Background(), reminder)
		require.Error(t, err)
	})

	t.Run("getRemindersForActorType", func(t *testing.T) {
		r1, r2, err := testReminders.getRemindersForActorType(context.Background(), "foo", false)
		assert.Nil(t, r1)
		assert.Nil(t, r2)
		require.Error(t, err)
	})

	t.Run("DeleteReminder", func(t *testing.T) {
		err := testReminders.DeleteReminder(context.Background(), internal.DeleteReminderRequest{})
		require.Error(t, err)
	})
}

func TestReminderCountFiring(t *testing.T) {
	actorType, actorID := getTestActorTypeAndID()

	testReminders := newTestReminders()
	defer testReminders.Close()
	clock := testReminders.clock.(*clocktesting.FakeClock)

	// Init a mock metrics collector
	activeCount := atomic.Int64{}
	invalidInvocations := atomic.Int64{}
	testReminders.SetMetricsCollectorFn(func(at string, r int64) {
		if at == actorType {
			activeCount.Store(r)
		} else {
			invalidInvocations.Add(1)
		}
	})

	// Count executions
	executereminderFnCount := atomic.Int64{}
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executereminderFnCount.Add(1)
		return true
	})

	testReminders.Init(context.Background())

	const numReminders = 6
	for i := 0; i < numReminders; i++ {
		req := internal.CreateReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      fmt.Sprintf("reminder%d", i),
			Data:      json.RawMessage(`"data"`),
			Period:    "10s",
			DueTime:   "10s",
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		require.NoError(t, testReminders.CreateReminder(context.Background(), reminder))
	}

	advanceTickers(t, clock, 500*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	const numPeriods = 8
	for i := 0; i < numPeriods; i++ {
		advanceTickers(t, clock, 10*time.Second)
		time.Sleep(100 * time.Millisecond)
	}

	assert.Equal(t, int64(0), invalidInvocations.Load())
	assert.Equal(t, int64(numReminders), activeCount.Load())

	assert.Equal(t, int64(numReminders*numPeriods), executereminderFnCount.Load())
}

func TestCreateTimerDueTimes(t *testing.T) {
	t.Run("create reminder with positive DueTime", func(t *testing.T) {
		provider := newTestReminders()
		defer provider.Close()
		clock := provider.clock.(*clocktesting.FakeClock)

		executed := make(chan string, 1)
		provider.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})

		req := internal.CreateReminderRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "myreminder",
			DueTime:   "1s",
		}
		r := createReminder(t, clock.Now(), req)

		err := provider.CreateReminder(context.Background(), r)
		require.NoError(t, err)

		advanceTickers(t, clock, 1*time.Second)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})

	t.Run("create reminder with 0 DueTime", func(t *testing.T) {
		provider := newTestReminders()
		defer provider.Close()
		clock := provider.clock.(*clocktesting.FakeClock)

		executed := make(chan string, 1)
		provider.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})

		req := internal.CreateReminderRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "myreminder",
			DueTime:   "0",
		}
		r := createReminder(t, clock.Now(), req)

		err := provider.CreateReminder(context.Background(), r)
		require.NoError(t, err)

		advanceTickers(t, clock, 10*time.Millisecond)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})

	t.Run("create reminder with no DueTime", func(t *testing.T) {
		provider := newTestReminders()
		defer provider.Close()
		clock := provider.clock.(*clocktesting.FakeClock)

		executed := make(chan string, 1)
		provider.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})

		req := internal.CreateReminderRequest{
			ActorID:   "myactor",
			ActorType: "mytype",
			Name:      "myreminder",
			DueTime:   "",
		}
		r := createReminder(t, clock.Now(), req)

		err := provider.CreateReminder(context.Background(), r)
		require.NoError(t, err)

		advanceTickers(t, clock, 10*time.Millisecond)
		select {
		case val := <-executed:
			assert.Equal(t, req.Key(), val)
		case <-time.After(10 * time.Second):
			t.Fatal("Did not receive a signal in time")
		}
	})
}

func TestSetReminderTrack(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.Init(context.Background())

	actorType, actorID := getTestActorTypeAndID()
	noRepetition := -1
	err := testReminders.updateReminderTrack(context.Background(), constructCompositeKey(actorType, actorID), noRepetition, testReminders.clock.Now(), nil)
	require.NoError(t, err)
}

func TestGetReminderTrack(t *testing.T) {
	t.Run("reminder doesn't exist", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		r, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorType, actorID))
		require.NoError(t, err)
		assert.Empty(t, r.LastFiredTime)
	})

	t.Run("reminder exists", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		repetition := 10
		now := testReminders.clock.Now()
		testReminders.updateReminderTrack(context.Background(), constructCompositeKey(actorType, actorID), repetition, now, nil)
		r, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorType, actorID))
		require.NoError(t, err)
		assert.NotEmpty(t, r.LastFiredTime)
		assert.Equal(t, repetition, r.RepetitionLeft)
		assert.Equal(t, now, r.LastFiredTime)
	})
}

func TestCreateReminder(t *testing.T) {
	numReminders := 100
	testReminders := newTestReminders()
	defer testReminders.Close()

	// Set the state store to not use locks when accessing data.
	// This will cause race conditions to surface when running these tests with `go test -race` if the methods accessing reminders' storage are not safe for concurrent access.
	store := daprt.NewFakeStateStore()
	store.NoLock = true
	testReminders.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return storeName, store, nil
	})
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, true)
		return true
	})
	testReminders.Init(context.Background())

	actorType, actorID := getTestActorTypeAndID()
	const secondActorType = "actor2"
	ctx := context.Background()

	// Create the reminders in parallel, which would surface race conditions if present
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()

		req := internal.CreateReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			Name:      "reminder0",
			Period:    "1s",
			DueTime:   "1s",
			TTL:       "PT10M",
			Data:      nil,
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)
	}()
	go func() {
		defer wg.Done()
		req := internal.CreateReminderRequest{
			ActorID:   actorID,
			ActorType: secondActorType,
			Name:      "reminder0",
			Period:    "1s",
			DueTime:   "1s",
			TTL:       "PT10M",
			Data:      nil,
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)
	}()
	wg.Wait()

	// Now creates new reminders and migrates the previous one.
	testRemindersWithPartition := newTestRemindersWithMockAndActorMetadataPartition()
	defer testRemindersWithPartition.Close()

	testRemindersWithPartition.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return storeName, store, nil
	})

	for i := 1; i < numReminders; i++ {
		for _, reminderActorType := range []string{actorType, secondActorType} {
			req := internal.CreateReminderRequest{
				ActorID:   actorID,
				ActorType: reminderActorType,
				Name:      "reminder" + strconv.Itoa(i),
				Period:    "1s",
				DueTime:   "1s",
				TTL:       "10m",
				Data:      nil,
			}
			reminder, err := req.NewReminder(testReminders.clock.Now())
			require.NoError(t, err)
			err = testRemindersWithPartition.CreateReminder(ctx, reminder)
			require.NoError(t, err)
		}
	}

	// Does not migrate yet
	_, actorTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), actorType, false)
	require.NoError(t, err)
	assert.NotEmpty(t, actorTypeMetadata.ID)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Check for 2nd type.
	_, actorTypeMetadata, err = testRemindersWithPartition.getRemindersForActorType(context.Background(), secondActorType, false)
	require.NoError(t, err)
	assert.NotEmpty(t, actorTypeMetadata.ID)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Migrates here.
	reminderReferences, actorTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), actorType, true)
	require.NoError(t, err)
	assert.NotEmpty(t, actorTypeMetadata.ID)
	assert.Equal(t, TestActorMetadataPartitionCount, actorTypeMetadata.RemindersMetadata.PartitionCount)

	partitions := map[uint32]bool{}
	reminders := map[string]bool{}
	for _, reminderRef := range reminderReferences {
		partition := reminderRef.ActorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.Reminder.Name] = true
		assert.Equal(t, actorTypeMetadata.ID, reminderRef.ActorMetadataID)
	}
	assert.Len(t, partitions, TestActorMetadataPartitionCount)
	assert.Len(t, reminderReferences, numReminders)
	assert.Len(t, reminders, numReminders)

	// Check for 2nd type.
	secondReminderReferences, secondTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), secondActorType, true)
	require.NoError(t, err)
	assert.NotEmpty(t, secondTypeMetadata.ID)
	assert.Equal(t, 20, secondTypeMetadata.RemindersMetadata.PartitionCount)

	partitions = map[uint32]bool{}
	reminders = map[string]bool{}
	for _, reminderRef := range secondReminderReferences {
		partition := reminderRef.ActorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.Reminder.Name] = true
		assert.Equal(t, secondTypeMetadata.ID, reminderRef.ActorMetadataID)
	}
	assert.Len(t, partitions, 20)
	assert.Len(t, secondReminderReferences, numReminders)
	assert.Len(t, reminders, numReminders)
}

func newTestRemindersWithMockAndActorMetadataPartition() *reminders {
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
	conf := internal.Config{
		AppID:                      TestAppID,
		ActorsService:              "placement:placement:5050",
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		HostedActorTypes:           internal.NewHostedActors(appConfig.Entities),
		Reentrancy:                 appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
		EntityConfigs:              make(map[string]internal.EntityConfig),
	}
	scanDuration, err := time.ParseDuration(appConfig.ActorScanInterval)
	if err == nil {
		conf.ActorDeactivationScanInterval = scanDuration
	}

	idleDuration, err := time.ParseDuration(appConfig.ActorIdleTimeout)
	if err == nil {
		conf.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(appConfig.DrainOngoingCallTimeout)
	if err == nil {
		conf.DrainOngoingCallTimeout = drainCallDuration
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := 32
		conf.Reentrancy.MaxStackDepth = &reentrancyLimit
	}

	// Make a map of the hosted actors so we can reference it below.
	hostedTypes := make(map[string]bool, len(appConfig.Entities))
	for _, hostedType := range appConfig.Entities {
		hostedTypes[hostedType] = true
	}

	for _, entityConfg := range appConfig.EntityConfigs {
		config := translateEntityConfig(entityConfg)
		for _, entity := range entityConfg.Entities {
			if _, ok := hostedTypes[entity]; ok {
				conf.EntityConfigs[entity] = config
			} else {
				log.Warnf("Configuration specified for non-hosted actor type: %s", entity)
			}
		}
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	apiLevel := &atomic.Uint32{}
	apiLevel.Store(internal.ActorAPILevel)
	r := NewRemindersProvider(internal.ActorsProviderOptions{
		Clock:    clock,
		Config:   conf,
		APILevel: apiLevel,
	})
	return r.(*reminders)
}

func translateEntityConfig(appConfig config.EntityConfig) internal.EntityConfig {
	domainConfig := internal.EntityConfig{
		Entities:                   appConfig.Entities,
		ActorIdleTimeout:           time.Minute * 60,
		DrainOngoingCallTimeout:    time.Second * 60,
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		ReentrancyConfig:           appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}

	idleDuration, err := time.ParseDuration(appConfig.ActorIdleTimeout)
	if err == nil {
		domainConfig.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(appConfig.DrainOngoingCallTimeout)
	if err == nil {
		domainConfig.DrainOngoingCallTimeout = drainCallDuration
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := 32
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}

func TestOverrideReminder(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		require.NoError(t, err)
		reminders, _, err := testReminders.getRemindersForActorType(ctx, actorType, false)
		require.NoError(t, err)
		require.Len(t, reminders, 1)
		assert.Equal(t, json.RawMessage(`"b"`), reminders[0].Reminder.Data)
	})

	t.Run("override dueTime", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "1s", "2s", "", "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		assert.Equal(t, testReminders.clock.Now().Add(2*time.Second), reminders[0].Reminder.RegisteredTime)
	})

	t.Run("override period", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "", "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		require.NoError(t, err)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		assert.Equal(t, "2s", reminders[0].Reminder.Period.String())
	})

	t.Run("override TTL", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "PT5M", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		ttl := "9999-09-01T00:00:00Z"
		origTime, err := time.Parse(time.RFC3339, ttl)
		require.NoError(t, err)
		req2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", ttl, "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		require.NoError(t, err)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		require.NotEmpty(t, reminders)
		assert.LessOrEqual(t, reminders[0].Reminder.ExpirationTime.Sub(origTime), 2*time.Second)
	})
}

func TestOverrideReminderCancelsActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		testReminders := newTestReminders()
		defer testReminders.Close()
		clock := testReminders.clock.(*clocktesting.FakeClock)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			requestC <- testRequest{Data: "c"}
			return true
		})
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		req := createReminderData(actorID, actorType, reminderName, "10s", "1s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, reminderName, "9s", "1s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "9s", reminders[0].Reminder.Period.String())
		assert.Equal(t, testReminders.clock.Now().Add(time.Second), reminders[0].Reminder.RegisteredTime)
		assert.Equal(t, json.RawMessage(`"b"`), reminders[0].Reminder.Data)

		req3 := createReminderData(actorID, actorType, reminderName, "8s", "2s", "", "c")
		reminder3, err := req3.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder3)
		reminders, _, err = testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "8s", reminders[0].Reminder.Period.String())
		assert.Equal(t, testReminders.clock.Now().Add(2*time.Second), reminders[0].Reminder.RegisteredTime)
		assert.Equal(t, json.RawMessage(`"c"`), reminders[0].Reminder.Data)

		// due time for reminder3 is 2s
		advanceTickers(t, clock, time.Second*2)

		// The reminder update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, string(reminders[0].Reminder.Data), "\""+request.Data.(string)+"\"")
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

// Makes tickers advance
func advanceTickers(t *testing.T, clock *clocktesting.FakeClock, step time.Duration) {
	t.Helper()

	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, 2*time.Second, 5*time.Millisecond, "ticker in program not created in time")
	clock.Step(step)
}

func TestOverrideReminderCancelsMultipleActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		requestC := make(chan testRequest, 10)
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			requestC <- testRequest{Data: "d"}
			return true
		})
		testReminders.Init(context.Background())

		start := testReminders.clock.Now()
		clock := testReminders.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		req := createReminderData(actorID, actorType, reminderName, "10s", "3s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		require.NoError(t, err)
		req3 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "c")
		reminder3, err := req3.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder3)
		require.NoError(t, err)

		// due time for reminders is 4s, advance less
		advanceTickers(t, clock, time.Second*2)

		// Check reminder is updated
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		// The statestore could have either reminder2 or reminder3 based on the timing.
		// Therefore, not verifying data field
		assert.Equal(t, "8s", reminders[0].Reminder.Period.String())
		assert.Equal(t, start.Add(4*time.Second), reminders[0].Reminder.RegisteredTime)

		req4 := createReminderData(actorID, actorType, reminderName, "7s", "2s", "", "d")
		reminder4, err := req4.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder4)
		reminders, _, err = testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)

		// due time for reminder is 2s
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		// The reminder update fires in a goroutine so we need to use the wall clock here
		select {
		case request := <-requestC:
			// Test that the last reminder update fired
			assert.Equal(t, string(reminders[0].Reminder.Data), "\""+request.Data.(string)+"\"")

			// Check reminder is updated
			assert.Equal(t, "7s", reminders[0].Reminder.Period.String())
			assert.Equal(t, start.Add(4*time.Second), reminders[0].Reminder.RegisteredTime)
			assert.Equal(t, json.RawMessage(`"d"`), reminders[0].Reminder.Data)
		case <-time.After(1500 * time.Millisecond):
			assert.Fail(t, "request channel timed out")
		}
	})
}

func TestDeleteReminderWithPartitions(t *testing.T) {
	testReminders := newTestRemindersWithMockAndActorMetadataPartition()
	defer testReminders.Close()
	stateStore := daprt.NewFakeStateStore()
	testReminders.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return storeName, stateStore, nil
	})
	testReminders.Init(context.Background())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()

	t.Run("Delete a reminder", func(t *testing.T) {
		// Create a reminder
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		require.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)
		assert.Len(t, testReminders.reminders[actorType], 1)

		// Delete the reminder
		startCount := stateStore.CallCount("Multi")
		err = testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "reminder1",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Empty(t, testReminders.reminders[actorType])

		// There should have been 1 Multi operation in the state store
		require.Equal(t, startCount+1, stateStore.CallCount("Multi"))
	})

	t.Run("Delete a reminder that doesn't exist", func(t *testing.T) {
		startCount := stateStore.CallCount("Multi")
		err := testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "does-not-exist",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Empty(t, testReminders.reminders[actorType])

		// There should have been no Multi operation in the state store
		require.Equal(t, startCount, stateStore.CallCount("Multi"))
	})
}

func TestDeleteReminder(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	// Set the state store to not use locks when accessing data.
	// This will cause race conditions to surface when running these tests with `go test -race` if the methods accessing reminders' storage are not safe for concurrent access.
	store := daprt.NewFakeStateStore()
	store.NoLock = true
	testReminders.SetStateStoreProviderFn(func() (string, internal.TransactionalStateStore, error) {
		return storeName, store, nil
	})
	testReminders.Init(context.Background())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()

	t.Run("Delete reminders in parallel should not have race conditions", func(t *testing.T) {
		// Create 2 reminders (in parallel)
		errs := make(chan error, 2)
		go func() {
			req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
			reminder, err := req.NewReminder(testReminders.clock.Now())
			require.NoError(t, err)
			errs <- testReminders.CreateReminder(ctx, reminder)
		}()
		go func() {
			req := createReminderData(actorID, actorType, "reminder2", "1s", "1s", "", "")
			reminder, err := req.NewReminder(testReminders.clock.Now())
			require.NoError(t, err)
			errs <- testReminders.CreateReminder(ctx, reminder)
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-errs)
		}
		assert.Len(t, testReminders.reminders[actorType], 2)

		// Delete the reminders (in parallel)
		startCount := store.CallCount("Multi")
		go func() {
			errs <- testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
				Name:      "reminder1",
				ActorID:   actorID,
				ActorType: actorType,
			})
		}()
		go func() {
			errs <- testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
				Name:      "reminder2",
				ActorID:   actorID,
				ActorType: actorType,
			})
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-errs)
		}
		assert.Empty(t, testReminders.reminders[actorType])

		// There should have been 2 Multi operations in the state store
		require.Equal(t, startCount+2, store.CallCount("Multi"))
	})

	t.Run("Delete a reminder that doesn't exist", func(t *testing.T) {
		startCount := store.CallCount("Multi")
		err := testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "does-not-exist",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Empty(t, testReminders.reminders[actorType])

		// There should have been no Multi operation in the state store
		require.Equal(t, startCount, store.CallCount("Multi"))
	})
}

func TestReminderRepeats(t *testing.T) {
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
			testReminders := newTestReminders()
			defer testReminders.Close()
			testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
				requestC <- testRequest{Data: "data"}
				return true
			})
			testReminders.Init(context.Background())

			clock := testReminders.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()

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

			req := internal.CreateReminderRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "reminder1",
				Period:    test.period,
				DueTime:   dueTime,
				TTL:       ttl,
				Data:      json.RawMessage(`"data"`),
			}
			reminder, err := req.NewReminder(testReminders.clock.Now())
			if test.expRepeats == 0 {
				require.ErrorContains(t, err, "has zero repetitions")
				return
			}
			require.NoError(t, err)
			err = testReminders.CreateReminder(ctx, reminder)
			require.NoError(t, err)

			testReminders.remindersLock.RLock()
			assert.Len(t, testReminders.reminders[actorType], 1)
			testReminders.remindersLock.RUnlock()

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
						require.NoError(t, testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
							Name:      reminder.Name,
							ActorID:   reminder.ActorID,
							ActorType: reminder.ActorType,
						}))
					}
					select {
					case request := <-requestC:
						// Decrease i since time hasn't increased.
						i--
						assert.Equal(t, string(reminder.Data), "\""+request.Data.(string)+"\"")
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

func TestReminderTTL(t *testing.T) {
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
			testReminders := newTestReminders()
			defer testReminders.Close()
			testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
				requestC <- testRequest{Data: "data"}
				return true
			})
			testReminders.Init(context.Background())

			clock := testReminders.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()

			var ttl string
			switch x := test.ttlAny.(type) {
			case string:
				ttl = x
			case int:
				ttl = clock.Now().Add(time.Duration(x) * time.Second).Format(time.RFC3339)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			t.Cleanup(cancel)

			req := internal.CreateReminderRequest{
				ActorID:   actorID,
				ActorType: actorType,
				Name:      "reminder1",
				Period:    test.period,
				DueTime:   test.dueTime,
				TTL:       ttl,
				Data:      json.RawMessage(`"data"`),
			}
			reminder, err := req.NewReminder(testReminders.clock.Now())
			require.NoError(t, err)
			err = testReminders.CreateReminder(ctx, reminder)
			require.NoError(t, err)

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
						assert.Equal(t, string(reminder.Data), "\""+request.Data.(string)+"\"")
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

func reminderValidation(dueTime, period, ttl, msg string) func(t *testing.T) {
	return func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()
		testReminders.Init(context.Background())

		actorType, actorID := getTestActorTypeAndID()

		req := createReminderData(actorID, actorType, "reminder4", period, dueTime, ttl, "data")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		if err == nil {
			err = testReminders.CreateReminder(context.Background(), reminder)
		}
		if len(msg) != 0 {
			require.ErrorContains(t, err, msg)
		} else {
			require.Error(t, err)
		}
	}
}

func TestReminderValidation(t *testing.T) {
	t.Run("empty period", reminderValidation("", "", "-2s", ""))
	t.Run("period is JSON null", reminderValidation("", "null", "-2s", ""))
	t.Run("period is empty JSON object", reminderValidation("", "{}", "-2s", ""))
	t.Run("reminder dueTime invalid (1)", reminderValidation("invalid", "R5/PT2S", "1h", "unsupported time/duration format: invalid"))
	t.Run("reminder dueTime invalid (2)", reminderValidation("R5/PT2S", "R5/PT2S", "1h", "repetitions are not allowed"))
	t.Run("reminder period invalid", reminderValidation(time.Now().Add(time.Minute).Format(time.RFC3339), "invalid", "1h", "unsupported duration format: invalid"))
	t.Run("reminder ttl invalid (1)", reminderValidation("", "", "invalid", "unsupported time/duration format: invalid"))
	t.Run("reminder ttl invalid (2)", reminderValidation("", "", "R5/PT2S", "repetitions are not allowed"))
	t.Run("reminder ttl expired (1)", reminderValidation("2s", "", "-2s", ""))
	t.Run("reminder ttl expired (2)", reminderValidation("", "", "-2s", ""))
	t.Run("reminder ttl expired (3)", reminderValidation(startOfTime.Add(2*time.Second).Format(time.RFC3339), "", startOfTime.Add(time.Second).Format(time.RFC3339), ""))
	t.Run("reminder ttl expired (4)", reminderValidation("", "", startOfTime.Add(-1*time.Second).Format(time.RFC3339), ""))
}

func TestGetReminder(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.Init(context.Background())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	require.NoError(t, err)
	testReminders.CreateReminder(ctx, reminder)
	assert.Len(t, testReminders.reminders[actorType], 1)
	r, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`"a"`), r.Data)
	assert.Equal(t, "1s", r.Period.String())
	assert.Equal(t, "1s", r.DueTime)
}

func TestReminderFires(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.Init(context.Background())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	req := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	require.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	require.NoError(t, err)

	advanceTickers(t, clock, time.Millisecond*101)

	actorKey := constructCompositeKey(actorType, actorID)
	assert.Eventually(t, func() bool {
		track, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
		require.NoError(t, err)
		require.NotNil(t, track)
		return !track.LastFiredTime.IsZero()
	}, time.Second, time.Millisecond)
}

func TestReminderDueDate(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.Init(context.Background())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	req := createReminderData(actorID, actorType, "reminder1", "100ms", "500ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	require.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	require.NoError(t, err)

	track, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
	require.NoError(t, err)
	assert.Empty(t, track.LastFiredTime)

	advanceTickers(t, clock, time.Millisecond*500)

	assert.Eventually(t, func() bool {
		track, err = testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
		require.NoError(t, err)
		require.NotNil(t, track)
		return !track.LastFiredTime.IsZero()
	}, time.Second, time.Millisecond)
}

func TestReminderPeriod(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.Init(context.Background())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	req := internal.CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder1",
		Period:    "100ms",
		DueTime:   "100ms",
		TTL:       "",
		Data:      json.RawMessage(`"a"`),
	}
	reminder, errRem := req.NewReminder(testReminders.clock.Now())
	require.NoError(t, errRem)
	require.NoError(t, testReminders.CreateReminder(ctx, reminder))

	advanceTickers(t, clock, 0)

	var (
		track  *internal.ReminderTrack
		track2 *internal.ReminderTrack
		err    error
	)

	assert.Eventually(t, func() bool {
		track, _ = testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
		require.NoError(t, err)
		require.NotNil(t, track)
		return !track.LastFiredTime.IsZero()
	}, time.Second, time.Millisecond)

	assert.Eventually(t, func() bool {
		advanceTickers(t, clock, time.Millisecond*100)
		track2, err = testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
		require.NoError(t, err)
		require.NotNil(t, track2)
		return !track2.LastFiredTime.IsZero() && track.LastFiredTime.Unix() != track2.LastFiredTime.Unix()
	}, time.Second, time.Millisecond)
}

func TestReminderFiresOnceWithEmptyPeriod(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.Init(context.Background())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	req := createReminderData(actorID, actorType, "reminder1", "", "100ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	require.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	require.NoError(t, err)

	clock.Step(100 * time.Millisecond)

	track, _ := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
	assert.Empty(t, track.LastFiredTime)
}

func TestCreateReminderGoroutineLeak(t *testing.T) {
	testReminders := newTestReminders()
	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()

	createFn := func(i int, ttl bool, dueTime string) error {
		req := &internal.CreateReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      fmt.Sprintf("reminder%d", i),
			Data:      json.RawMessage(`"data"`),
			DueTime:   dueTime,
		}
		if ttl {
			req.DueTime = "1s"
			req.Period = "1s"
			req.TTL = "2s"
		}
		reminder, err := req.NewReminder(clock.Now())
		if err != nil {
			return err
		}
		return testReminders.CreateReminder(context.Background(), reminder)
	}

	// Get the baseline goroutines
	initialCount := runtime.NumGoroutine()

	// Create 10 reminders with unique names
	for i := 0; i < 10; i++ {
		require.NoError(t, createFn(i, false, "2s"))
	}

	// Create 5 reminders that override the first ones
	for i := 0; i < 5; i++ {
		require.NoError(t, createFn(i, false, "2s"))
	}

	// Create 5 reminders that have TTLs
	for i := 10; i < 15; i++ {
		require.NoError(t, createFn(i, true, "2s"))
	}

	// Create 5 reminders with a long due time
	for i := 15; i < 20; i++ {
		require.NoError(t, createFn(i, false, "1h"))
	}

	// Advance the clock to make the first reminders fire
	time.Sleep(150 * time.Millisecond)
	clock.Sleep(5 * time.Second)
	time.Sleep(150 * time.Millisecond)
	clock.Sleep(5 * time.Second)

	// Get the number of goroutines again, which should be +/- 2 the initial one (we give it some buffer) plus 5, because we still have 5 active reminders
	expectCount := initialCount + 5
	require.Eventuallyf(t, func() bool {
		currentCount := runtime.NumGoroutine()
		return currentCount < (expectCount+2) && currentCount > (expectCount-2)
	}, time.Second, 50*time.Millisecond, "Current number of goroutine %[1]d is outside of range [%[2]d-2, %[2]d+2] (current count may be stale)", time.Duration(runtime.NumGoroutine()), expectCount)

	// Stop the provider
	require.NoError(t, testReminders.Close())

	// Get the number of goroutines again, which should be +/- 2 the initial one (we give it some buffer)
	require.Eventuallyf(t, func() bool {
		currentCount := runtime.NumGoroutine()
		return currentCount < (initialCount+2) && currentCount > (initialCount-2)
	}, time.Second, 50*time.Millisecond, "Current number of goroutine %[1]d is outside of range [%[2]d-2, %[2]d+2] (current count may be stale)", time.Duration(runtime.NumGoroutine()), initialCount)
}

func getTestActorTypeAndID() (string, string) {
	return "cat", "e485d5de-de48-45ab-816e-6cc700d18ace"
}

func createReminderData(actorID, actorType, name, period, dueTime, ttl, data string) internal.CreateReminderRequest {
	r := internal.CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		TTL:       ttl,
	}
	if data != "" {
		r.Data = json.RawMessage(`"` + data + `"`)
	}
	return r
}

func createReminder(t *testing.T, now time.Time, req internal.CreateReminderRequest) *internal.Reminder {
	t.Helper()

	reminder, err := req.NewReminder(now)
	require.NoError(t, err)

	return reminder
}
