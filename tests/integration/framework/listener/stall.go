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

package listener

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Stall wraps a net.Listener and holds on to accepted connections before
// handing them to the server, simulating an application which is slow to
// complete its protocol handshake, for example a busy app whose accept loop is
// starved. It also keeps hold of the connections it has accepted so a test can
// drop them on demand and force the client to dial again.
type Stall struct {
	net.Listener

	stall atomic.Int64

	lock     sync.Mutex
	accepted []net.Conn
}

// New returns a Stall wrapping the given listener.
func New(ln net.Listener) *Stall {
	return &Stall{Listener: ln}
}

// SetStall sets how long Accept holds a connection before returning it. Zero
// disables stalling.
func (s *Stall) SetStall(d time.Duration) {
	s.stall.Store(int64(d))
}

// CloseAccepted closes every connection accepted so far.
func (s *Stall) CloseAccepted() {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, conn := range s.accepted {
		conn.Close()
	}
	s.accepted = nil
}

func (s *Stall) Accept() (net.Conn, error) {
	conn, err := s.Listener.Accept()
	if err != nil {
		return nil, err
	}

	if stall := s.stall.Load(); stall > 0 {
		time.Sleep(time.Duration(stall))
	}

	s.lock.Lock()
	s.accepted = append(s.accepted, conn)
	s.lock.Unlock()

	return conn, nil
}
