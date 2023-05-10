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
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
)

func init() {
	suite.Register(new(Ports))
}

// Ports tests that the ports are available when the app is running.
type Ports struct{}

func (p *Ports) Setup(t *testing.T) []framework.RunDaprdOption {
	return nil
}

func (p *Ports) Run(t *testing.T, cmd *framework.Command) {
	for name, port := range map[string]int{
		"app":           cmd.AppPort,
		"grpc":          cmd.GrpcPort,
		"http":          cmd.HttpPort,
		"metrics":       cmd.MetricsPort,
		"internal-grpc": cmd.InternalGRPCPort,
		"public":        cmd.PublicPort,
	} {
		assert.Eventuallyf(t, func() bool {
			_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
			return err == nil
		}, time.Second, time.Millisecond, "port %s (:%d) was not available in time", name, port)
	}
}
