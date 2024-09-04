/*
Copyright 2024 The Dapr Authors
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

	require.Len(t, decoded.Inbox, 1)
	require.Equal(t, int32(-1), decoded.Inbox[0].GetEventId())
	require.Equal(t, timestamppb.New(time1), decoded.Inbox[0].GetTimestamp())

	require.Len(t, decoded.History, 1)
	require.Equal(t, int32(2), decoded.History[0].GetEventId())
	require.Equal(t, timestamppb.New(time2), decoded.History[0].GetTimestamp())

	require.Equal(t, wfs.CustomStatus, decoded.CustomStatus)
	require.Equal(t, wfs.Generation, decoded.Generation)
}
