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

import (
	"github.com/dapr/dapr/pkg/placement/internal/loops"
)

// recvLoop is the main loop for receiving messages from the stream. It handles
// errors and calls the recv function to receive messages.
func (s *stream) recvLoop() error {
	for {
		err := s.recv()
		if err != nil {
			log.Warnf("Error receiving from stream %s: %s", s.addr, err)
			return err
		}
	}
}

// recv receives a message from the stream. It checks whether this host needs
// to disseminate the namespace.
func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	if err != nil {
		return err
	}

	// TODO: @joshvanl: we can potentially cache the client ID from the stream
	// context after the first message.
	if err = s.authz.Host(s.channel, resp); err != nil {
		log.Warnf("Authorization failed for stream %s: %v", s.addr, err)
		return err
	}

	s.nsLoop.Enqueue(&loops.ReportedHost{
		Host:      resp,
		StreamIDx: s.idx,
	})

	return nil
}
