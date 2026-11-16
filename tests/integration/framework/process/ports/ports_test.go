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

package ports

import (
	"errors"
	"net"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePortRange(t *testing.T) {
	floor, err := parsePortRange([]byte("32768\t60999\n"))
	require.NoError(t, err)
	assert.Equal(t, 32768, floor)

	for _, in := range []string{"", "32768", "abc\t60999\n"} {
		_, err = parsePortRange([]byte(in))
		require.Error(t, err, in)
	}
}

func TestEphemeralCeil(t *testing.T) {
	assert.Equal(t, 16384, ephemeralCeil(16384, nil))
	assert.Equal(t, 32768, ephemeralCeil(32768, nil))
	assert.Equal(t, 49000, ephemeralCeil(49152, nil))
	assert.Equal(t, 49000, ephemeralCeil(1024, nil))
	assert.Equal(t, 49000, ephemeralCeil(0, errors.New("no sysctl")))
}

func TestPortsCeil(t *testing.T) {
	assert.Less(t, portsBase(), portsCeil)
}

func TestFreeWaitsForInFlightFork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Free waits for forks on linux only")
	}

	p := Reserve(t, 1)

	// Holding ForkLock stands in for a fork in flight, whose child holds a
	// copy of the reserved listener until it has exec'd.
	syscall.ForkLock.Lock()
	start := time.Now()
	go func() {
		time.Sleep(time.Millisecond * 200)
		syscall.ForkLock.Unlock()
	}()

	p.Free(t)
	assert.GreaterOrEqual(t, time.Since(start), time.Millisecond*200)
}

func TestFreeWaitsForExecingChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Free waits for forks on linux only")
	}

	p := Reserve(t, 1)
	port := p.Port(t)

	// A duplicate of the reserved listener stands in for the copy a child
	// holds until its exec has finished, after it released ForkLock.
	dup, err := resvP[p.start].ln.(*net.TCPListener).File()
	require.NoError(t, err)
	start := time.Now()
	go func() {
		time.Sleep(time.Millisecond * 200)
		dup.Close()
	}()

	p.Free(t)
	assert.GreaterOrEqual(t, time.Since(start), time.Millisecond*200)

	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}
