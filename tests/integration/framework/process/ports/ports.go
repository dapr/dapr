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

package ports

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	lock     sync.Mutex
	resvPLen int
	resvPIx  int
	last     = 1024
	resvP    []*reservedPort
)

const blockSize = 500

// Ports reserves network ports, and then frees them when the test is ready to
// run so a  process can bind to them at runtime.
type Ports struct {
	idx   int
	start int
	end   int
	freed atomic.Bool
}

type reservedPort struct {
	ln   net.Listener
	port int
}

func Reserve(t *testing.T, count int) *Ports {
	t.Helper()

	lock.Lock()
	defer lock.Unlock()

	require.GreaterOrEqual(t, count, 1)
	require.LessOrEqual(t, count, 20)

	resvPLen -= count
	if count > resvPLen || resvPLen < 20 {
		t.Logf("reserving %d more ports", blockSize)
		for i := 0; i < blockSize; i++ {
			last++
			if last+i > 65535 {
				last = 1024
			}
			ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(last))
			if err != nil {
				i--
				continue
			}
			tcp, ok := ln.Addr().(*net.TCPAddr)
			require.True(t, ok)
			resvP = append(resvP, &reservedPort{ln, tcp.Port})
		}
		resvPLen += blockSize
	}

	p := &Ports{
		idx:   resvPIx,
		start: resvPIx,
		end:   resvPIx + count,
	}
	resvPIx += count

	t.Cleanup(func() { p.Free(t) })

	return p
}

func (p *Ports) Port(t *testing.T) int {
	t.Helper()

	lock.Lock()
	defer func() {
		p.idx++
		lock.Unlock()
	}()

	require.Less(t, p.idx, p.end, "request for more ports than reserved")

	return resvP[p.idx].port
}

func (p *Ports) Listener(t *testing.T) net.Listener {
	t.Helper()

	lock.Lock()
	defer func() {
		p.idx++
		lock.Unlock()
	}()

	require.Less(t, p.idx, p.end, "request for more ports than reserved")

	return resvP[p.idx].ln
}

func (p *Ports) Free(t *testing.T) {
	t.Helper()
	if p.freed.CompareAndSwap(false, true) {
		lock.Lock()
		defer lock.Unlock()
		for i := p.start; i < p.end; i++ {
			err := resvP[i].ln.Close()
			if !errors.Is(err, net.ErrClosed) {
				require.NoError(t, err)
			}
		}
	}
}

func (p *Ports) Run(t *testing.T, _ context.Context) {
	t.Helper()
	p.Free(t)
}

func (p *Ports) Cleanup(t *testing.T) {
	p.Free(t)
}
