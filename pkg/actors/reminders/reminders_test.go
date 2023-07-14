package reminders

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/stretchr/testify/assert"
	clocktesting "k8s.io/utils/clock/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

func newTestReminders() *reminders {
	opts := ReminderOpts{
		StoreName:   "testStore",
		HostAddress: "localhost",
		Port:        50001,
	}
	clock := clocktesting.NewFakeClock(startOfTime)
	r := NewRemindersProvider(clock, opts)
	r.SetStateStoreProvider(fakeStore)
	return r.(*reminders)
}

func fakeStore() (internal.TransactionalStateStore, error) {
	storeS := daprt.NewFakeStateStore()
	storeName := "actorStore"

	compStore := compstore.New()
	compStore.AddStateStore(storeName, storeS)

	storeS, ok = compStore.GetStateStore(storeName)
	store, ok := storeS.(internal.TransactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return nil, errors.New(errStateStoreNotConfigured)
	}

	return store, nil
}

func TestStoreIsNotInitialized(t *testing.T) {
	testActorsRuntime := newTestReminders()
	// defer testActorsRuntime.Stop()
	// for name := range testActorsRuntime.compStore.ListStateStores() {
	// 	testActorsRuntime.compStore.DeleteStateStore(name)
	// }

	t.Run("getReminderTrack", func(t *testing.T) {
		r, err := testActorsRuntime.getReminderTrack(context.Background(), "foo||bar")
		assert.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("updateReminderTrack", func(t *testing.T) {
		err := testActorsRuntime.updateReminderTrack(context.Background(), "foo||bar", 1, testActorsRuntime.clock.Now(), nil)
		assert.Error(t, err)
	})

	// t.Run("CreateReminder", func(t *testing.T) {
	// 	err := testActorsRuntime.CreateReminder(context.Background(), &CreateReminderRequest{})
	// 	assert.Error(t, err)
	// })

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

// func TestReminderExecution(t *testing.T) {
// 	testActorsRuntime := newTestActorsRuntime()
// 	defer testActorsRuntime.Stop()

// 	actorType, actorID := getTestActorTypeAndID()
// 	fakeCallAndActivateActor(testActorsRuntime, actorType, actorID, testActorsRuntime.clock)

// 	period, _ := internal.NewReminderPeriod("2s")
// 	err := testActorsRuntime.executeReminder(&internal.Reminder{
// 		ActorType:      actorType,
// 		ActorID:        actorID,
// 		RegisteredTime: time.Now().Add(2 * time.Second),
// 		Period:         period,
// 		Name:           "reminder1",
// 		Data:           json.RawMessage(`"data"`),
// 	}, false)
// 	assert.NoError(t, err)
// }
