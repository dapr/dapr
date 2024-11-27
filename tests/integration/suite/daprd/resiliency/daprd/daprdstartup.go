/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package daprd

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprdstartup))
}

type daprdstartup struct {
	logline *logline.LogLine
	dapr    *daprd.Daprd
}

func (s *daprdstartup) Setup(t *testing.T) []framework.Option {
	s.logline = logline.New(t, logline.WithStdoutLineContains(
		"Could not read resiliency policy myresiliency",
	))

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 10ms
        maxRetries: 3
        matching:
          gRPCStatusCodes: 400
          httpStatusCodes: 11
`
	s.dapr = daprd.New(t,
		daprd.WithResourceFiles(resiliency),
		daprd.WithLogLineStdout(s.logline),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.dapr),
	}
}

func (s *daprdstartup) Run(t *testing.T, ctx context.Context) {
	s.dapr.WaitUntilRunning(t, ctx)

	t.Run("daprd process continue when resiliency is invalid", func(t *testing.T) {
		s.logline.EventuallyFoundAll(t)
	})
}
