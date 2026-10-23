/*
Copyright 2023 The Dapr Authors
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
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/phayes/freeport"
)

// GetStablePort returns an available TCP port.
// It first attempts to get a free port that is calculated in a deterministic way based on the current process' environment, capped to the valid TCP port range.
// Ports in exclude are never returned: if the stable pick is excluded or busy, it falls back to a random port.
// If no available port can be found, it returns a random port (which could be outside of the range defined above).
func GetStablePort(start int, appID string, exclude ...int) (int, error) {
	// Try determining a "stable" port
	// The algorithm considers the following elements to generate a "random-looking", but stable, port number:
	// - app-id: app-id values should be unique within a solution
	// - Current working directory: allows separating different projects a developer is working on
	// - ID of the current user: allows separating different users
	// - Hostname: this is also different within each container
	parts := make([]string, 4)
	parts[0] = appID
	parts[1], _ = os.Getwd()
	parts[2] = strconv.Itoa(os.Getuid())
	parts[3], _ = os.Hostname()
	base := []byte(strings.Join(parts, "|"))

	// Compute the SHA-256 hash to generate a "random", but stable, sequence of binary values
	h := sha256.Sum256(base)

	// Use the first 16 bits of the hash as a "random number", then modulo
	// by the remaining port range to ensure the result is a valid TCP port.
	maxOffset := 65535 - start
	if maxOffset <= 0 {
		return freePortSkipping(exclude)
	}
	rnd := (int(h[0]) | int(h[1])<<8) % maxOffset
	port := start + rnd

	if !slices.Contains(exclude, port) {
		// Check if the port is available
		addr, err := net.ResolveTCPAddr("tcp", "localhost:"+strconv.Itoa(port))
		if err != nil {
			// Consider these as hard errors, as it should never happen that a port can't be resolved
			return 0, err
		}

		// Try listening on that port; if it works, assume the port is available
		l, err := net.ListenTCP("tcp", addr)
		if err == nil {
			l.Close()
			return port, nil
		}
	}

	// Fall back to returning a random port
	return freePortSkipping(exclude)
}

// maxFreePortAttempts bounds how often freePortSkipping retries when the OS
// keeps handing back an excluded port.
const maxFreePortAttempts = 10

func freePortSkipping(exclude []int) (int, error) {
	for range maxFreePortAttempts {
		p, err := freeport.GetFreePort()
		if err != nil {
			return 0, err
		}
		if !slices.Contains(exclude, p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port found outside the excluded ports after %d attempts", maxFreePortAttempts)
}
