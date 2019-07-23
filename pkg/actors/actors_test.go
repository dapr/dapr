package actors

import (
	"sync"
	"testing"
	"time"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/channel/http"
)

type fakeAppChannel struct {
}

func (f *fakeAppChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	return &channel.InvokeResponse{
		Metadata: map[string]string{http.HTTPStatusCode: "200"},
	}, nil
}

func newTestActorsRuntime(t *testing.T) *actorsRuntime {
	ch := fakeChannel()
	config := NewConfig("", "", "", nil, 0, "", "")
	a := NewActors(nil, ch, nil, config)

	return a.(*actorsRuntime)
}

func getTestActorTypeAndID() (string, string) {
	return "cat", "hobbit"
}

func fakeChannel() channel.AppChannel {
	return &fakeAppChannel{}
}

func fakeCallAndActivateActor(actors *actorsRuntime, actorKey string) {
	actors.actorsTable.LoadOrStore(actorKey, &actor{
		lastUsedTime: time.Now(),
		lock:         &sync.RWMutex{},
		active:       false,
	})
}

func deactivateActorWithDuration(testActorsRuntime *actorsRuntime, actorKey string, actorIdleTimeout time.Duration) {
	fakeCallAndActivateActor(testActorsRuntime, actorKey)
	scanInterval := time.Second * 1
	testActorsRuntime.startDeactivationTicker(scanInterval, actorIdleTimeout)
}

func TestActorIsDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime(t)
	idleTimeout := time.Second * 2
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorKey, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	if exists {
		t.Fail()
	}
}

func TestActorIsNotDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime(t)
	idleTimeout := time.Second * 5
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorKey, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	if !exists {
		t.Fail()
	}
}
