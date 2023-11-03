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
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/phayes/freeport"
)

// GetStablePort returns an available TCP port.
// It first attempts to get a free port that is between start and start+2047, which is calculated in a deterministic way based on the current process' environment.
// If no available port can be found, it returns a random port (which could be outside of the range defined above).
func GetStablePort(start int, appID string) (int, error) {
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

	// Get the first 11 bits (0-2047) as "random number"
	rnd := int(h[0]) + int(h[1]>>5)<<8
	port := start + rnd

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

	// Fall back to returning a random port
	return freeport.GetFreePort()
}
