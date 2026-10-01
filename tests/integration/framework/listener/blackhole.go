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
	"sync/atomic"
)

// Blackhole simulates a network partition. Once enabled, its connections stay
// open but everything sent in either direction is silently discarded, so the
// peer sees no error and no connection reset.
type Blackhole struct {
	net.Listener

	enabled atomic.Bool
}

func NewBlackhole(ln net.Listener) *Blackhole {
	return &Blackhole{Listener: ln}
}

func (b *Blackhole) Enable() {
	b.enabled.Store(true)
}

func (b *Blackhole) Accept() (net.Conn, error) {
	conn, err := b.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &blackholeConn{Conn: conn, enabled: &b.enabled}, nil
}

type blackholeConn struct {
	net.Conn

	enabled *atomic.Bool
}

// Read also discards data that arrives during an already blocked read.
func (c *blackholeConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if err != nil || !c.enabled.Load() {
			return n, err
		}
	}
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.enabled.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}
