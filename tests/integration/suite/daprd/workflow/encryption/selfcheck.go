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

package encryption

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(selfcheck))
}

// selfcheck configures a key the crypto component does not hold. daprd
// refuses to run workflows rather than store payloads it cannot encrypt.
type selfcheck struct {
	*fworkflow.Cluster
	logline *logline.LogLine
}

func (s *selfcheck) Setup(t *testing.T) []framework.Option {
	s.Cluster = fworkflow.NewCluster(t)
	s.logline = logline.New(t, logline.WithStdoutLineContains(
		"workflow payload encryption is enabled but its key is not usable",
	))
	d := s.Daprd(t, append(fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "missing.json", false),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(s.logline),
	)...)
	return append([]framework.Option{framework.WithProcesses(s.logline)}, s.Options(d)...)
}

func (s *selfcheck) Run(t *testing.T, ctx context.Context) {
	s.logline.EventuallyFoundAll(t)
}
