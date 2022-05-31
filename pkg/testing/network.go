package testing

import (
	"net"
	"testing"
	"time"
)

// WaitForListeningAddress waits for `addresses` to be listening for up to `timeout`.
func WaitForListeningAddress(t *testing.T, timeout time.Duration, addresses ...string) {
	start := time.Now().UTC()

	for _, address := range addresses {
		t.Logf("Waiting for address %q", address)
	check:
		for {
			d := timeout - time.Since(start)
			if d <= 0 {
				t.Log("Waiting for addresses timed out")
				t.FailNow()
			}

			conn, _ := net.DialTimeout("tcp", address, d)
			if conn != nil {
				conn.Close()

				break check
			}

			time.Sleep(time.Millisecond * 500)
		}
		t.Logf("Address %q is ready", address)
	}
}

// GetFreePorts asks the kernel for `num` free open ports that are ready to use.
// This code is retrofitted from freeport.GetFreePort().
func GetFreePorts(num uint) ([]int, error) {
	ports := make([]int, num)
	for i := uint(0); i < num; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, err
		}
		defer l.Close()

		ports[i] = l.Addr().(*net.TCPAddr).Port
	}

	return ports, nil
}
