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

package stream

import "github.com/dapr/dapr/pkg/actors/internal/placement/loops"

// recvLoop is the main loop for receiving messages from the stream. It handles
// errors and calls the recv function to receive messages.
func (s *stream) recvLoop() error {
	for {
		err := s.recv()
		if err != nil {
			// "not a leader" rejections are expected when a daprd
			// happens to round-robin onto a non-leader placement
			// replica - it'll cycle to another replica on the next
			// reconnect. Logging this at warning level on every
			// cycle spams the runtime log during placement leader
			// churn; demote to debug.
			if loops.IsTransientLeaderError(err) {
				log.Debugf("Error receiving from stream: %s", err)
			} else {
				log.Warnf("Error receiving from stream: %s", err)
			}
			return err
		}
	}
}

func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	if err != nil {
		return err
	}

	s.placeLoop.Enqueue(&loops.StreamOrder{
		IDx:   s.idx,
		Order: resp,
	})

	return nil
}
