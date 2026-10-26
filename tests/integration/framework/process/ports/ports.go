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
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

var (
	lock     sync.Mutex
	resvPLen int
	resvPIx  int
	last     = portsBase()
	resvP    []*reservedPort
	// recentlyFreed records when Free released each port. A process binds
	// its ports only after Free, so a probe that has wrapped around must not
	// take one before that process has had the chance to.
	recentlyFreed = map[int]time.Time{}
)

const freedGrace = 30 * time.Second

// portsBase returns the starting port for reservation probing. A test binary
// re-executed inside an unshare user namespace (where our mapped euid is 0)
// probes the top fifth of the range to avoid colliding with the parent
// process's reservations on the lower port range.
func portsBase() int {
	if os.Geteuid() == 0 {
		return portsCeil - portsCeil/5
	}
	return 1024
}

const blockSize = 500

// portsCeil is the exclusive upper bound for reservation probing: the first
// port of the OS ephemeral range, capped at maxPortsCeil. Probing only below
// that range keeps held-open reserved listeners off ports the kernel assigns
// for outbound dials (exhausting that pool surfaces as EADDRNOTAVAIL, "can't
// assign requested address"), and stops the kernel from handing a port that
// Free just released to an outbound dial as its source port before the
// process that reserved it binds it ("bind: address already in use"). The
// macOS CI job lowers the range to start at 16384, and Linux defaults to
// 32768.
var portsCeil = ephemeralCeil(ephemeralFloor())

const maxPortsCeil = 49000

func ephemeralCeil(floor int, err error) int {
	if err != nil || floor < 8192 {
		fmt.Fprintf(os.Stderr, "ports: cannot use OS ephemeral port floor %d (err: %v), reserving below %d\n", floor, err, maxPortsCeil)
		return maxPortsCeil
	}
	return min(floor, maxPortsCeil)
}

// parsePortRange returns the first port of a Linux ip_local_port_range value.
func parsePortRange(b []byte) (int, error) {
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, fmt.Errorf("unexpected port range %q", b)
	}
	return strconv.Atoi(f[0])
}

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
		iowriter.Eventf(t, "reserving %d more ports", blockSize)
		for i := 0; i < blockSize; i++ {
			last++
			if last+i >= portsCeil {
				last = portsBase()
			}
			if freedAt, ok := recentlyFreed[last]; ok {
				if time.Since(freedAt) < freedGrace {
					i--
					continue
				}
				delete(recentlyFreed, last)
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
	if !p.freed.CompareAndSwap(false, true) {
		return
	}

	freed := make([]int, 0, p.end-p.start)
	func() {
		lock.Lock()
		defer lock.Unlock()
		for i := p.start; i < p.end; i++ {
			err := resvP[i].ln.Close()
			if !errors.Is(err, net.ErrClosed) {
				require.NoError(t, err)
			}
			recentlyFreed[resvP[i].port] = time.Now()
			freed = append(freed, resvP[i].port)
		}
	}()

	// A child forked while the listeners were open holds copies of them until
	// its exec has finished, and the process the ports go to fails to bind
	// them meanwhile. forkExec holds ForkLock until its vfork child has
	// started to exec, so taking it waits for those children; the probes then
	// wait out the rest of their exec, and no fork can copy a probe while
	// ForkLock is held. Linux only: darwin releases ForkLock before its child
	// execs, and its net.Listen takes ForkLock itself.
	if runtime.GOOS != "linux" {
		return
	}
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	for _, port := range freed {
		waitBindable(port)
	}
}

// waitBindable waits, for up to 2s, until port can be bound.
func waitBindable(port int) {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			ln.Close()
			return
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
