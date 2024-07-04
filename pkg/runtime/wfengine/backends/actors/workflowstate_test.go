package actors

import (
	"testing"
	"time"

	"github.com/microsoft/durabletask-go/backend"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWorkflowStateSerialization(t *testing.T) {

	time1 := time.Now()
	time2 := time.Now().Add(3 * time.Hour).Add(3 * time.Minute)

	wfs := NewWorkflowState(NewActorsBackendConfig("foo"))
	wfs.AddToInbox(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time1),
	})
	wfs.History = append(wfs.History, &backend.HistoryEvent{
		EventId:   2,
		Timestamp: timestamppb.New(time2),
	})
	wfs.CustomStatus = "baz"
	wfs.Generation = 33

	encoded, err := wfs.EncodeWorkflowState()
	require.NoError(t, err)

	var decoded workflowState
	err = decoded.DecodeWorkflowState(encoded)
	require.NoError(t, err)

	require.Equal(t, 1, len(decoded.Inbox))
	require.Equal(t, decoded.Inbox[0].EventId, int32(-1))
	require.Equal(t, decoded.Inbox[0].Timestamp, timestamppb.New(time1))

	require.Equal(t, 1, len(decoded.History))
	require.Equal(t, decoded.History[0].EventId, int32(2))
	require.Equal(t, decoded.History[0].Timestamp, timestamppb.New(time2))

	require.Equal(t, wfs.CustomStatus, decoded.CustomStatus)
	require.Equal(t, wfs.Generation, decoded.Generation)

}
