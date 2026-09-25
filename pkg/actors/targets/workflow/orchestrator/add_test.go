/*
Copyright 2026 The Dapr Authors
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

package orchestrator

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/x509svid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/kit/crypto/spiffe/signer"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// Test_addWorkflowEvent_dedupReAssertsReminder is a focused regression test for
// a stuck-workflow class: under
// fleet-wide daprd kill, activity-result events end up durable in state.Inbox
// while the wake-up reminder that should drive runWorkflow has been deleted
// from the scheduler (acked SUCCESS on a prior round). The next time the
// activity reminder retries and publishResult calls into addWorkflowEvent, the
// dedup check fires (the event is already in inbox) and the function returns
// nil. Without the re-assert, the activity reminder is then acked SUCCESS and
// also removed, leaving the inbox row stranded with no reminder to drive it.
//
// This test pins the fix: when dedup hits, addWorkflowEvent must (idempotently,
// by deterministic name) re-create the wake-up reminder before returning.
func Test_addWorkflowEvent_dedupReAssertsReminder(t *testing.T) {
	const (
		instanceID = "test-dedup-wf"
		scheduled  = int32(7)
	)

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`null`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}

	taskScheduled := &protos.HistoryEvent{
		EventId:   scheduled,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	}

	history := []*backend.HistoryEvent{startEvent, taskScheduled}

	// The dedup-trigger: an activity result event already sitting in the inbox.
	// In production this row was put there by a prior addWorkflowEvent call
	// whose corresponding wake-up reminder has since been acked SUCCESS by the
	// scheduler and deleted.
	durableCompletion := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: scheduled,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}

	wfState := state.NewState(state.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		wfState.AddToHistory(e)
	}
	wfState.AddToInbox(durableCompletion)

	// Capture every CreateReminder call so we can assert the wake-up reminder
	// gets re-created on the dedup path even though no state mutation happens.
	var (
		mu       sync.Mutex
		creates  []*actorapi.CreateReminderRequest
		fakeRems = remindersfake.New().
				WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
				mu.Lock()
				defer mu.Unlock()
				creates = append(creates, req)
				return nil
			})
	)

	actors := fake.New().WithReminders(func(context.Context) (reminders.Interface, error) {
		return fakeRems, nil
	})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)

	// Prime the orchestrator with the durable state we constructed above so
	// loadInternalState returns it without touching a real state store.
	o.state = wfState
	o.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())

	// The second arrival of the same activity result: a publishResult retry
	// because the original activity reminder was not acked.
	incoming := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: scheduled,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}

	require.NoError(t, o.addWorkflowEvent(t.Context(), incoming, completionSender{}))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, creates, 1,
		"dedup-drop path must re-assert exactly one wake-up reminder so the inbox row is not stranded")

	got := creates[0]
	assert.Equal(t, "dapr.internal.default.testapp.workflow", got.ActorType,
		"reminder must target the workflow actor that holds the inbox row")
	assert.Equal(t, instanceID, got.ActorID)

	wantName := "new-event-tc-" + strconv.Itoa(int(scheduled))
	assert.Equal(t, wantName, got.Name,
		"reminder name must be the deterministic new-event-tc-<TaskScheduledId> so retries collapse onto a single scheduler entry rather than accumulating")
	assert.True(t, strings.HasPrefix(got.Name, reminderPrefixNewEvent+"-"),
		"reminder name must use the new-event prefix so handleReminder routes it to runWorkflowFromReminder")

	// State must not have been mutated: dedup branch returns without
	// AddToInbox / signAndSaveState.
	assert.Len(t, wfState.Inbox, 1,
		"dedup branch must not append to the inbox (the duplicate is already there)")
	assert.Len(t, wfState.History, len(history),
		"dedup branch must not touch history")
}

// Test_addWorkflowEvent_unknownTaskIDDroppedNotTombstoned pins the signing
// behavior for a completion that fails verification while the durable state
// is gone (the fake store is empty): dropped with ErrInstanceNotFound, no
// tombstone, nothing persisted. The sentinel classification itself is pinned
// by the signing package unit tests and the canstraggler integration test.
func Test_addWorkflowEvent_unknownTaskIDDroppedNotTombstoned(t *testing.T) {
	const instanceID = "test-unknown-task-wf"

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`null`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}
	history := []*backend.HistoryEvent{startEvent}

	wfState := state.NewState(state.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		wfState.AddToHistory(e)
	}

	var (
		mu       sync.Mutex
		creates  []*actorapi.CreateReminderRequest
		fakeRems = remindersfake.New().
				WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
				mu.Lock()
				defer mu.Unlock()
				creates = append(creates, req)
				return nil
			})
	)
	// The drop path corroborates against durable state before classifying;
	// answer that probe with an empty store (the statefake default returns a
	// nil response, which the loader does not expect from a real store).
	fakeState := statefake.New().WithGetFn(func(context.Context, *actorapi.GetStateRequest, bool) (*actorapi.StateResponse, error) {
		return &actorapi.StateResponse{}, nil
	})
	actors := fake.New().WithReminders(func(context.Context) (reminders.Interface, error) {
		return fakeRems, nil
	}).WithState(func(context.Context) (actorstate.Interface, error) {
		return fakeState, nil
	})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
		Signer:            testAddSigner(t),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	o.state = wfState
	o.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())

	// A completion for a task never scheduled in this history. The attestation
	// only needs to be non-nil: the unknown-id check precedes verification.
	incoming := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 42,
				Result:          wrapperspb.String(`"late"`),
				Attestation:     &backend.ActivityCompletionAttestation{},
			},
		},
	}

	err = o.addWorkflowEvent(t.Context(), incoming, completionSender{})
	require.ErrorIs(t, err, api.ErrInstanceNotFound)

	assert.False(t, wfState.HasTamperMarker(),
		"an unknown task scheduled id must not tombstone the workflow as tampered")
	assert.Empty(t, wfState.Inbox, "the dropped completion must not be persisted")
	assert.Len(t, wfState.History, len(history), "history must be untouched")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, creates, "no wake-up reminder for a dropped event")
}

func testAddSigner(t *testing.T) *signer.Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		URIs:         []*url.URL{{Scheme: "spiffe", Host: "example.org", Path: "/ns/default/app-a"}},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	require.NoError(t, err)
	certs, err := x509.ParseCertificates(certDER)
	require.NoError(t, err)
	id, err := x509svid.IDFromCert(certs[0])
	require.NoError(t, err)
	return signer.New(staticTestSVIDSource{svid: &x509svid.SVID{
		ID:           id,
		Certificates: certs,
		PrivateKey:   priv,
	}}, nil)
}

type staticTestSVIDSource struct {
	svid *x509svid.SVID
}

func (s staticTestSVIDSource) GetX509SVID() (*x509svid.SVID, error) {
	return s.svid, nil
}
