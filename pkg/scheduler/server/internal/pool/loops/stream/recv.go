/*
Copyright 2025 The Dapr Authors
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

package stream

import (
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// recvLoop is the main loop for receiving messages from the stream. It
// handles errors and calls the recv function to receive messages.
func (s *stream) recvLoop() {
	for {
		err := s.recv()
		if err == nil {
			continue
		}

		isEOF := errors.Is(err, io.EOF)
		status, ok := status.FromError(err)
		if s.channel.Context().Err() != nil || isEOF || (ok && status.Code() != codes.Canceled) {
			return
		}

		log.Warnf("Error receiving from stream %s/%s: %s", s.ns, s.appID, err)
		return
	}
}

// recv receives a message from the stream. It checks the result and calls
// the inflight result function with the appropriate result.
func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	if err != nil {
		return err
	}

	result := resp.GetResult()
	if result == nil {
		return errors.New("received nil result from stream")
	}

	inf, ok := s.inflight.LoadAndDelete(result.GetId())
	if !ok {
		return errors.New("received unknown trigger response from stream")
	}

	switch result.GetStatus() {
	case schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS:
		inf.(func(api.TriggerResponseResult))(api.TriggerResponseResult_SUCCESS)
	case schedulerv1pb.WatchJobsRequestResultStatus_FAILED:
		inf.(func(api.TriggerResponseResult))(api.TriggerResponseResult_FAILED)
	default:
		return fmt.Errorf("unknown trigger response status: %s", result.GetStatus())
	}

	return nil
}
