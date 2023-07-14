package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal"
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
)

func newTestReminders() *reminders {
	conf := internal.Config{
		AppID:              TestAppID,
		PlacementAddresses: []string{"placement:5050"},
	}
	opts := ReminderOpts{
		StoreName: "testStore",
		Config:    conf,
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	r := NewRemindersProvider(clock, opts)
	r.SetStateStoreProvider(fakeTStore)
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
	return nil, errors.New("state store not configured")
}

func fakeRealTStore() (internal.TransactionalStateStore, error) {
	return daprt.NewFakeStateStore(), nil
}

// daprt.NewFakeStateStore()

func TestStoreIsNotInitialized(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	// for name := range testActorsRuntime.compStore.ListStateStores() {
	// 	testActorsRuntime.compStore.DeleteStateStore(name)
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
		reminder, err := req.NewReminder(time.Now())
		assert.NoError(t, err)
		err = testReminders.CreateReminder(context.Background(), reminder)
		assert.Error(t, err)
	})

	// t.Run("getRemindersForActorType", func(t *testing.T) {
	// 	r1, r2, err := testActorsRuntime.getRemindersForActorType(context.Background(), "foo", false)
	// 	assert.Nil(t, r1)
	// 	assert.Nil(t, r2)
	// 	assert.Error(t, err)
	// })

	// t.Run("DeleteReminder", func(t *testing.T) {
	// 	err := testActorsRuntime.DeleteReminder(context.Background(), &DeleteReminderRequest{})
	// 	assert.Error(t, err)
	// })

	// t.Run("RenameReminder", func(t *testing.T) {
	// 	err := testActorsRuntime.RenameReminder(context.Background(), &RenameReminderRequest{})
	// 	assert.Error(t, err)
	// })
}

func TestReminderCountFiring(t *testing.T) {
	testReminders := newTestReminders()
	defer testReminders.Close()
	testReminders.SetStateStoreProvider(fakeRealTStore)
	executed := make(chan string, 1)
	testReminders.SetExecuteReminderFn(func(reminder *internal.Reminder) bool {
		executed <- reminder.Key()
		return true
	})
	testReminders.SetLookupActorFn(func(string, string) (bool, string) {
		return true, "localhost"
	})
	testReminders.Init(context.TODO())
	// testActorsRuntime := newTestActorsRuntime()
	// defer testActorsRuntime.Stop()

	actorType, actorID := getTestActorTypeAndID()
	// fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

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
		reminder, err := req.NewReminder(time.Now())
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
	diagtestutils.RequireTagExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(true)))
	diagtestutils.RequireTagNotExist(t, rows, diagtestutils.NewTag("success", strconv.FormatBool(false)))
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
