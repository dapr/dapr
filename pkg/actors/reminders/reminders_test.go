package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/diagnostics/diagtestutils"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	clocktesting "k8s.io/utils/clock/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	TestAppID                       = "fakeAppID"
	TestKeyName                     = "key0"
	TestPodName                     = "testPodName"
	TestActorMetadataPartitionCount = 3

	actorTimersLastValueViewName     = "runtime/actor/timers"
	actorRemindersLastValueViewName  = "runtime/actor/reminders"
	actorTimersFiredTotalViewName    = "runtime/actor/timers_fired_total"
	actorRemindersFiredTotalViewName = "runtime/actor/reminders_fired_total"
	errStateStoreNotConfigured       = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

func newTestReminders() *reminders {
	conf := internal.Config{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		HostedActorTypes:   []string{"cat"},
	}
	opts := internal.RemindersProviderOpts{
		StoreName: "testStore",
		Config:    conf,
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	r := NewRemindersProvider(clock, opts)
	r.SetStateStoreProviderFn(fakeTStore)
	return r.(*reminders)
}

// func fakeStore() (internal.TransactionalStateStore, error) {
// 	storeS := daprt.NewFakeStateStore()
// 	storeName := "actorStore"

// 	compStore := compstore.New()
// 	compStore.AddStateStore(storeName, storeS)

// 	storeS, ok = compStore.GetStateStore(storeName)
// 	store, ok := storeS.(internal.TransactionalStateStore)
// 	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
// 		return nil, errors.New(errStateStoreNotConfigured)
// 	}

// 	return store, nil
// }

func fakeTStore() (internal.TransactionalStateStore, error) {
	return nil, errors.New(errStateStoreNotConfigured)
}

var fakeStor internal.TransactionalStateStore

func fakeRealTStore() (internal.TransactionalStateStore, error) {
	if fakeStor == nil {
		fakeStor = daprt.NewFakeStateStore()
	}
	return fakeStor, nil
}

var fakeStor2 internal.TransactionalStateStore

func fakeRealTStore2() (internal.TransactionalStateStore, error) {
	if fakeStor2 == nil {
		fakeStor2 = daprt.NewFakeStateStore()
	}
	return fakeStor2, nil
}

func fakeRealTStoreWithNoLock() (internal.TransactionalStateStore, error) {
	if fakeStor == nil {
		fakeStor = daprt.NewFakeStateStore()
	}
	fakeStor.(*daprt.FakeStateStore).NoLock = true
	return fakeStor, nil
}

// testRequest is the request object that encapsulates the `data` field of a request.
type testRequest struct {
	Data any `json:"data"`
}

// daprt.NewFakeStateStore()

func TestStoreIsNotInitialized(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	// for name := range testReminders.compStore.ListStateStores() {
	// 	testReminders.compStore.DeleteStateStore(name)
	// }

	t.Run("getReminderTrack", func(t *testing.T) {
		r, err := testReminders.getReminderTrack(context.Background(), "foo||bar")
		assert.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("updateReminderTrack", func(t *testing.T) {
		err := testReminders.updateReminderTrack(context.Background(), "foo||bar", 1, testReminders.clock.Now(), nil)
		assert.Error(t, err)
	})

	t.Run("CreateReminder", func(t *testing.T) {
		req := internal.CreateReminderRequest{}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(context.Background(), reminder)
		assert.Error(t, err)
	})

	// t.Run("getRemindersForActorType", func(t *testing.T) {
	// 	r1, r2, err := testReminders.getRemindersForActorType(context.Background(), "foo", false)
	// 	assert.Nil(t, r1)
	// 	assert.Nil(t, r2)
	// 	assert.Error(t, err)
	// })

	// t.Run("DeleteReminder", func(t *testing.T) {
	// 	err := testReminders.DeleteReminder(context.Background(), &DeleteReminderRequest{})
	// 	assert.Error(t, err)
	// })

	// t.Run("RenameReminder", func(t *testing.T) {
	// 	err := testReminders.RenameReminder(context.Background(), &RenameReminderRequest{})
	// 	assert.Error(t, err)
	// })
}

func TestReminderCountFiring(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	var executereminderFnCount int64 = 0
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executereminderFnCount++
		diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, true)
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()

	// init default service metrics where actor metrics are registered
	assert.NoError(t, diag.DefaultMonitoring.Init(testReminders.config.AppID))
	t.Cleanup(func() {
		metricsCleanup()
	})

	numReminders := 10

	for i := 0; i < numReminders; i++ {
		req := internal.CreateReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      fmt.Sprintf("reminder%d", i),
			Data:      json.RawMessage(`"data"`),
			Period:    "10s",
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		require.NoError(t, testReminders.CreateReminder(context.Background(), reminder))
	}

	time.Sleep(200 * time.Millisecond)
	testReminders.clock.Sleep(500 * time.Millisecond)
	numPeriods := 20
	for i := 0; i < numPeriods; i++ {
		testReminders.clock.Sleep(10 * time.Second)
		time.Sleep(50 * time.Millisecond)
	}

	rows, err := view.RetrieveData("runtime/actor/reminders")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, int64(numReminders), int64(rows[0].Data.(*view.LastValueData).Value))

	// check metrics recorded
	rows, err = view.RetrieveData(actorRemindersFiredTotalViewName)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, int64(numReminders*numPeriods), rows[0].Data.(*view.CountData).Value)
	assert.Equal(t, int64(numReminders*numPeriods), executereminderFnCount)
	diagtestutils.RequireTagExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(true)))
	diagtestutils.RequireTagNotExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(false)))
}

func TestReminderCountFiringBad(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	var executereminderFnCount int64 = 0
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executereminderFnCount++
		diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, false)
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()

	// init default service metrics where actor metrics are registered
	assert.NoError(t, diag.DefaultMonitoring.Init(testReminders.config.AppID))

	numReminders := 2

	for i := 0; i < numReminders; i++ {
		req := internal.CreateReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      fmt.Sprintf("reminder%d", i),
			Data:      json.RawMessage(`"data"`),
			Period:    "10s",
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		require.NoError(t, testReminders.CreateReminder(context.Background(), reminder))
	}

	time.Sleep(200 * time.Millisecond)
	testReminders.clock.Sleep(500 * time.Millisecond)
	numPeriods := 5
	for i := 0; i < numPeriods; i++ {
		testReminders.clock.Sleep(10 * time.Second)
		time.Sleep(50 * time.Millisecond)
	}

	rows, err := view.RetrieveData(actorRemindersLastValueViewName)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, int64(numReminders), int64(rows[0].Data.(*view.LastValueData).Value))

	// check metrics recorded
	rows, err = view.RetrieveData(actorRemindersFiredTotalViewName)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, int64(numReminders*numPeriods), rows[0].Data.(*view.CountData).Value)
	assert.Equal(t, int64(numReminders*numPeriods), executereminderFnCount)
	diagtestutils.RequireTagExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(false)))
	diagtestutils.RequireTagNotExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(true)))
}

func TestSetReminderTrack(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()
	noRepetition := -1
	err := testReminders.updateReminderTrack(context.Background(), constructCompositeKey(actorType, actorID), noRepetition, testReminders.clock.Now(), nil)
	assert.NoError(t, err)
}

func TestGetReminderTrack(t *testing.T) {
	t.Run("reminder doesn't exist", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		r, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorType, actorID))
		require.NoError(t, err)
		assert.Empty(t, r.LastFiredTime)
	})

	t.Run("reminder exists", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

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

// TD: FIX
func TestCreateReminder(t *testing.T) {
	numReminders := 100
	// appChannel := new(mockAppChannel)
	testReminders := newTestReminders()
	defer testReminders.Close()

	// Set the state store to not use locks when accessing data.
	// This will cause race conditions to surface when running these tests with `go test -race` if the methods accessing reminders' storage are not safe for concurrent access.
	// stateStore, _ := testReminders.stateStore()
	// stateStore.(*daprt.FakeStateStore).NoLock = true

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStoreWithNoLock)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, true)
		return false
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

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
		assert.NoError(t, err)
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
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)
	}()
	wg.Wait()

	// Now creates new reminders and migrates the previous one.
	testRemindersWithPartition := newTestRemindersWithMockAndActorMetadataPartition()
	defer testRemindersWithPartition.Close()

	fakeStor2 = nil
	testRemindersWithPartition.SetStateStoreProviderFn(fakeRealTStore2)
	testRemindersWithPartition.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		// executed <- reminder.Key()
		return false
	})
	testRemindersWithPartition.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testRemindersWithPartition.Init(context.TODO())

	// testActorsRuntimeWithPartition.compStore = testReminders.compStore
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
			assert.NoError(t, err)
			err = testRemindersWithPartition.CreateReminder(ctx, reminder)
			require.NoError(t, err)
		}
	}

	// Does not migrate yet
	_, actorTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), actorType, false)
	require.NoError(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Check for 2nd type.
	_, actorTypeMetadata, err = testRemindersWithPartition.getRemindersForActorType(context.Background(), secondActorType, false)
	require.NoError(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, 0, actorTypeMetadata.RemindersMetadata.PartitionCount)

	// Migrates here.
	reminderReferences, actorTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), actorType, true)
	require.NoError(t, err)
	assert.True(t, len(actorTypeMetadata.ID) > 0)
	assert.Equal(t, TestActorMetadataPartitionCount, actorTypeMetadata.RemindersMetadata.PartitionCount)

	partitions := map[uint32]bool{}
	reminders := map[string]bool{}
	for _, reminderRef := range reminderReferences {
		partition := reminderRef.ActorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.Reminder.Name] = true
		assert.Equal(t, actorTypeMetadata.ID, reminderRef.ActorMetadataID)
	}
	assert.Equal(t, TestActorMetadataPartitionCount, len(partitions))
	assert.Equal(t, numReminders, len(reminderReferences))
	assert.Equal(t, numReminders, len(reminders))

	// Check for 2nd type.
	secondReminderReferences, secondTypeMetadata, err := testRemindersWithPartition.getRemindersForActorType(context.Background(), secondActorType, true)
	require.NoError(t, err)
	assert.True(t, len(secondTypeMetadata.ID) > 0)
	assert.Equal(t, 20, secondTypeMetadata.RemindersMetadata.PartitionCount)

	partitions = map[uint32]bool{}
	reminders = map[string]bool{}
	for _, reminderRef := range secondReminderReferences {
		partition := reminderRef.ActorRemindersPartitionID
		partitions[partition] = true
		reminders[reminderRef.Reminder.Name] = true
		assert.Equal(t, secondTypeMetadata.ID, reminderRef.ActorMetadataID)
	}
	assert.Equal(t, 20, len(partitions))
	assert.Equal(t, numReminders, len(secondReminderReferences))
	assert.Equal(t, numReminders, len(reminders))
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
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
		// HostedActorTypes:   []string{"cat"},
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		HostedActorTypes:           appConfig.Entities,
		Reentrancy:                 appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}
	opts := internal.RemindersProviderOpts{
		StoreName: "testStore",
		Config:    conf,
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	r := NewRemindersProvider(clock, opts)
	// r.SetStateStoreProviderFn(fakeTStore)
	return r.(*reminders)
}

func TestRenameReminder(t *testing.T) {
	// appChannel := new(mockAppChannel)
	testReminders := newTestReminders()
	defer testReminders.Close()

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStoreWithNoLock)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	errs := make(chan error, 2)
	retrieved := make(chan *internal.Reminder, 2)

	// Set the state store to not use locks when accessing data.
	// This will cause race conditions to surface when running these tests with `go test -race` if the methods accessing reminders' storage are not safe for concurrent access.
	// stateStore, _ := testReminders.stateStore()
	// stateStore.(*daprt.FakeStateStore).NoLock = true

	// Create 2 reminders in parallel
	go func() {
		req := internal.CreateReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			Name:      "reminder0",
			Period:    "1s",
			DueTime:   "1s",
			TTL:       "PT10M",
			Data:      json.RawMessage(`"a"`),
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		errs <- testReminders.CreateReminder(ctx, reminder)
	}()
	go func() {
		req := internal.CreateReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			Name:      "reminderA",
			Period:    "10s",
			DueTime:   "10s",
			TTL:       "PT10M",
			Data:      json.RawMessage(`"b"`),
		}
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		errs <- testReminders.CreateReminder(ctx, reminder)
	}()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	require.Equal(t, 2, len(testReminders.reminders[actorType]))

	// Rename reminders, in parallel
	go func() {
		errs <- testReminders.RenameReminder(ctx, &internal.RenameReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			OldName:   "reminder0",
			NewName:   "reminder1",
		})
	}()
	go func() {
		errs <- testReminders.RenameReminder(ctx, &internal.RenameReminderRequest{
			ActorID:   actorID,
			ActorType: actorType,
			OldName:   "reminderA",
			NewName:   "reminderB",
		})
	}()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	assert.Equal(t, 2, len(testReminders.reminders[actorType]))

	// Verify that the reminders retrieved with the old name no longer exists (in parallel)
	go func() {
		reminder, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      "reminder0",
		})
		errs <- err
		retrieved <- reminder
	}()
	go func() {
		reminder, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      "reminderA",
		})
		errs <- err
		retrieved <- reminder
	}()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	for i := 0; i < 2; i++ {
		require.Nil(t, <-retrieved)
	}

	// Verify that the reminders retrieved with the new name already exists (in parallel)
	go func() {
		reminder, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      "reminder1",
		})
		errs <- err
		retrieved <- reminder
	}()
	go func() {
		reminder, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Name:      "reminderB",
		})
		errs <- err
		retrieved <- reminder
	}()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	for i := 0; i < 2; i++ {
		reminder := <-retrieved
		require.NotNil(t, reminder)
		if string(reminder.Data) == `"a"` {
			assert.Equal(t, "1s", reminder.Period.String())
			assert.Equal(t, "1s", reminder.DueTime)
		} else if string(reminder.Data) == `"b"` {
			assert.Equal(t, "10s", reminder.Period.String())
			assert.Equal(t, "10s", reminder.DueTime)
		} else {
			t.Fatal("Found unexpected reminder:", reminder)
		}
	}
}

func TestOverrideReminder(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
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

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		assert.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "1s", "2s", "", "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		assert.Equal(t, testReminders.clock.Now().Add(2*time.Second), reminders[0].Reminder.RegisteredTime)
	})

	t.Run("override period", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		assert.NoError(t, err)

		req2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "", "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		assert.Equal(t, "2s", reminders[0].Reminder.Period.String())
	})

	t.Run("override TTL", func(t *testing.T) {
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		req := createReminderData(actorID, actorType, "reminder1", "2s", "1s", "PT5M", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		assert.NoError(t, err)

		ttl := "9999-09-01T00:00:00Z"
		origTime, err := time.Parse(time.RFC3339, ttl)
		assert.NoError(t, err)
		req2 := createReminderData(actorID, actorType, "reminder1", "2s", "1s", ttl, "")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		require.NotEmpty(t, reminders)
		assert.LessOrEqual(t, reminders[0].Reminder.ExpirationTime.Sub(origTime), 2*time.Second)
	})
}

func TestOverrideReminderCancelsActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		// requestC := make(chan testRequest, 10)
		// appChannel := mockAppChannel{
		// 	requestC: requestC,
		// }
		testReminders := newTestReminders()
		defer testReminders.Close()
		// clock := testReminders.clock.(*clocktesting.FakeClock)

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		req := createReminderData(actorID, actorType, reminderName, "10s", "1s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		assert.NoError(t, err)

		req2 := createReminderData(actorID, actorType, reminderName, "9s", "1s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder2)
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		require.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "9s", reminders[0].Reminder.Period.String())
		assert.Equal(t, testReminders.clock.Now().Add(time.Second), reminders[0].Reminder.RegisteredTime)
		assert.Equal(t, json.RawMessage(`"b"`), reminders[0].Reminder.Data)

		req3 := createReminderData(actorID, actorType, reminderName, "8s", "2s", "", "c")
		reminder3, err := req3.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder3)
		reminders, _, err = testReminders.getRemindersForActorType(context.Background(), actorType, false)
		assert.NoError(t, err)
		// Check reminder is updated
		assert.Equal(t, "8s", reminders[0].Reminder.Period.String())
		assert.Equal(t, testReminders.clock.Now().Add(2*time.Second), reminders[0].Reminder.RegisteredTime)
		assert.Equal(t, json.RawMessage(`"c"`), reminders[0].Reminder.Data)

		// due time for reminder3 is 2s
		// advanceTickers(t, clock, time.Second*2)

		// // The reminder update fires in a goroutine so we need to use the wall clock here
		// select {
		// case request := <-requestC:
		// 	// Test that the last reminder update fired
		// 	assert.Equal(t, string(reminders[0].Reminder.Data), "\""+request.Data.(string)+"\"")
		// case <-time.After(1500 * time.Millisecond):
		// 	assert.Fail(t, "request channel timed out")
		// }
	})
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

func TestOverrideReminderCancelsMultipleActiveReminders(t *testing.T) {
	ctx := context.Background()
	t.Run("override data", func(t *testing.T) {
		// requestC := make(chan testRequest, 10)
		// appChannel := mockAppChannel{
		// 	requestC: requestC,
		// }
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		start := testReminders.clock.Now()
		clock := testReminders.clock.(*clocktesting.FakeClock)

		actorType, actorID := getTestActorTypeAndID()
		reminderName := "reminder1"

		req := createReminderData(actorID, actorType, reminderName, "10s", "3s", "", "a")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder)
		assert.NoError(t, err)

		req2 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "b")
		reminder2, err := req2.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder2)
		assert.NoError(t, err)
		req3 := createReminderData(actorID, actorType, reminderName, "8s", "4s", "", "c")
		reminder3, err := req3.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(ctx, reminder3)
		assert.NoError(t, err)

		// due time for reminders is 4s, advance less
		advanceTickers(t, clock, time.Second*2)

		// Check reminder is updated
		reminders, _, err := testReminders.getRemindersForActorType(context.Background(), actorType, false)
		assert.NoError(t, err)
		// The statestore could have either reminder2 or reminder3 based on the timing.
		// Therefore, not verifying data field
		assert.Equal(t, "8s", reminders[0].Reminder.Period.String())
		assert.Equal(t, start.Add(4*time.Second), reminders[0].Reminder.RegisteredTime)

		req4 := createReminderData(actorID, actorType, reminderName, "7s", "2s", "", "d")
		reminder4, err := req4.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		testReminders.CreateReminder(ctx, reminder4)
		reminders, _, err = testReminders.getRemindersForActorType(context.Background(), actorType, false)
		assert.NoError(t, err)

		// due time for reminder is 2s
		advanceTickers(t, clock, time.Second)
		advanceTickers(t, clock, time.Second)

		// // The reminder update fires in a goroutine so we need to use the wall clock here
		// select {
		// case request := <-requestC:
		// 	// Test that the last reminder update fired
		// 	assert.Equal(t, string(reminders[0].Reminder.Data), "\""+request.Data.(string)+"\"")

		// 	// Check reminder is updated
		// 	assert.Equal(t, "7s", reminders[0].Reminder.Period.String())
		// 	assert.Equal(t, start.Add(4*time.Second), reminders[0].Reminder.RegisteredTime)
		// 	assert.Equal(t, json.RawMessage(`"d"`), reminders[0].Reminder.Data)
		// case <-time.After(1500 * time.Millisecond):
		// 	assert.Fail(t, "request channel timed out")
		// }
	})
}

func TestDeleteReminderWithPartitions(t *testing.T) {
	// appChannel := new(mockAppChannel)
	testReminders := newTestRemindersWithMockAndActorMetadataPartition()
	defer testReminders.Close()
	// stateStore, _ := testReminders.stateStore()
	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()

	t.Run("Delete a reminder", func(t *testing.T) {
		// Create a reminder
		req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		err = testReminders.CreateReminder(ctx, reminder)
		require.NoError(t, err)
		assert.Equal(t, 1, len(testReminders.reminders[actorType]))

		// Delete the reminder
		startCount := fakeStor.(*daprt.FakeStateStore).CallCount("Multi")
		err = testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "reminder1",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, len(testReminders.reminders[actorType]))

		// There should have been 1 Multi operation in the state store
		require.Equal(t, startCount+1, fakeStor.(*daprt.FakeStateStore).CallCount("Multi"))
	})

	t.Run("Delete a reminder that doesn't exist", func(t *testing.T) {
		startCount := fakeStor.(*daprt.FakeStateStore).CallCount("Multi")
		err := testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "does-not-exist",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, len(testReminders.reminders[actorType]))

		// There should have been no Multi operation in the state store
		require.Equal(t, startCount, fakeStor.(*daprt.FakeStateStore).CallCount("Multi"))
	})
}

func TestDeleteReminder(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	// Set the state store to not use locks when accessing data.
	// This will cause race conditions to surface when running these tests with `go test -race` if the methods accessing reminders' storage are not safe for concurrent access.
	// stateStore, _ := testReminders.stateStore()
	// stateStore.(*daprt.FakeStateStore).NoLock = true

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStoreWithNoLock)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()

	t.Run("Delete reminders in parallel should not have race conditions", func(t *testing.T) {
		// Create 2 reminders (in parallel)
		errs := make(chan error, 2)
		go func() {
			req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "")
			reminder, err := req.NewReminder(testReminders.clock.Now())
			assert.NoError(t, err)
			errs <- testReminders.CreateReminder(ctx, reminder)
		}()
		go func() {
			req := createReminderData(actorID, actorType, "reminder2", "1s", "1s", "", "")
			reminder, err := req.NewReminder(testReminders.clock.Now())
			assert.NoError(t, err)
			errs <- testReminders.CreateReminder(ctx, reminder)
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-errs)
		}
		assert.Equal(t, 2, len(testReminders.reminders[actorType]))

		// Delete the reminders (in parallel)
		startCount := fakeStor.(*daprt.FakeStateStore).CallCount("Multi")
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
		assert.Equal(t, 0, len(testReminders.reminders[actorType]))

		// There should have been 2 Multi operations in the state store
		require.Equal(t, startCount+2, fakeStor.(*daprt.FakeStateStore).CallCount("Multi"))
	})

	t.Run("Delete a reminder that doesn't exist", func(t *testing.T) {
		startCount := fakeStor.(*daprt.FakeStateStore).CallCount("Multi")
		err := testReminders.DeleteReminder(ctx, internal.DeleteReminderRequest{
			Name:      "does-not-exist",
			ActorID:   actorID,
			ActorType: actorType,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, len(testReminders.reminders[actorType]))

		// There should have been no Multi operation in the state store
		require.Equal(t, startCount, fakeStor.(*daprt.FakeStateStore).CallCount("Multi"))
	})
}

// TD: FIX
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
			// appChannel := mockAppChannel{
			// 	requestC: requestC,
			// }
			testReminders := newTestReminders()
			defer testReminders.Close()

			fakeStor = nil
			testReminders.SetStateStoreProviderFn(fakeRealTStore)
			// executed := make(chan string, 1)
			testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
				// executed <- reminder.Key()
				return true
			})
			testReminders.SetLookupActorFn(func(string, string) (bool, string) {
				return true, "localhost"
			})
			testReminders.Init(context.TODO())

			// t.Cleanup(testReminders.Stop)
			clock := testReminders.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			// fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, clock)

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
			reminder, _ := req.NewReminder(testReminders.clock.Now())
			err := testReminders.CreateReminder(ctx, reminder)
			if test.expRepeats == 0 {
				assert.ErrorContains(t, err, "has zero repetitions")
				return
			}
			require.NoError(t, err)

			testReminders.remindersLock.RLock()
			assert.Equal(t, 1, len(testReminders.reminders[actorType]))
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

// TD: FIX
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
			// appChannel := mockAppChannel{
			// 	requestC: requestC,
			// }
			testReminders := newTestReminders()
			defer testReminders.Close()

			fakeStor = nil
			testReminders.SetStateStoreProviderFn(fakeRealTStore)
			// executed := make(chan string, 1)
			testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
				// executed <- reminder.Key()
				return false
			})
			testReminders.SetLookupActorFn(func(string, string) (bool, string) {
				return true, "localhost"
			})
			testReminders.Init(context.TODO())

			clock := testReminders.clock.(*clocktesting.FakeClock)

			actorType, actorID := getTestActorTypeAndID()
			// fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, clock)

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
			reminder, _ := req.NewReminder(testReminders.clock.Now())
			err := testReminders.CreateReminder(ctx, reminder)
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
		// requestC := make(chan testRequest, 10)
		// appChannel := mockAppChannel{
		// 	requestC: requestC,
		// }
		testReminders := newTestReminders()
		defer testReminders.Close()

		fakeStor = nil
		testReminders.SetStateStoreProviderFn(fakeRealTStore)
		executed := make(chan string, 1)
		testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
			executed <- reminder.Key()
			return true
		})
		testReminders.SetLookupActorFn(func(string, string) (bool, string) {
			return true, "localhost"
		})
		testReminders.Init(context.TODO())

		actorType, actorID := getTestActorTypeAndID()
		// fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testReminders.clock)

		req := createReminderData(actorID, actorType, "reminder4", period, dueTime, ttl, "data")
		reminder, err := req.NewReminder(testReminders.clock.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(context.Background(), reminder)
		if len(msg) != 0 {
			assert.ErrorContains(t, err, msg)
		} else {
			assert.Error(t, err)
		}
	}
}

// TD: FIX
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

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	req := createReminderData(actorID, actorType, "reminder1", "1s", "1s", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	assert.NoError(t, err)
	testReminders.CreateReminder(ctx, reminder)
	assert.Equal(t, 1, len(testReminders.reminders[actorType]))
	r, err := testReminders.GetReminder(ctx, &internal.GetReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.NoError(t, err)
	assert.Equal(t, json.RawMessage(`"a"`), r.Data)
	assert.Equal(t, "1s", r.Period.String())
	assert.Equal(t, "1s", r.DueTime)
}

func TestReminderFires(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	req := createReminderData(actorID, actorType, "reminder1", "100ms", "100ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	assert.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	assert.NoError(t, err)

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

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	req := createReminderData(actorID, actorType, "reminder1", "100ms", "500ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	assert.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	assert.NoError(t, err)

	track, err := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
	assert.NoError(t, err)
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

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

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
	assert.NoError(t, errRem)
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

	fakeStor = nil
	testReminders.SetStateStoreProviderFn(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())

	clock := testReminders.clock.(*clocktesting.FakeClock)

	actorType, actorID := getTestActorTypeAndID()
	ctx := context.Background()
	actorKey := constructCompositeKey(actorType, actorID)
	req := createReminderData(actorID, actorType, "reminder1", "", "100ms", "", "a")
	reminder, err := req.NewReminder(testReminders.clock.Now())
	assert.NoError(t, err)
	err = testReminders.CreateReminder(ctx, reminder)
	assert.NoError(t, err)

	clock.Step(100 * time.Millisecond)

	track, _ := testReminders.getReminderTrack(context.Background(), constructCompositeKey(actorKey, "reminder1"))
	assert.Empty(t, track.LastFiredTime)
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
func metricsCleanup() {
	diagtestutils.CleanupRegisteredViews(
		actorRemindersLastValueViewName,
		actorTimersLastValueViewName,
		actorRemindersFiredTotalViewName,
		actorRemindersFiredTotalViewName)
}
