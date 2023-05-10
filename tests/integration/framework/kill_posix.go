//go:build !windows
// +build !windows

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

package framework

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func (c *Command) interrupt(t *testing.T) {
	// TODO: daprd does not currently gracefully exit on a single interrupt
	// signal. Remove once fixed.
	assert.NoError(t, c.cmd.Process.Signal(os.Interrupt))
	time.Sleep(time.Millisecond * 300)
	assert.NoError(t, c.cmd.Process.Signal(os.Interrupt))
}
