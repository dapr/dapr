/*
Copyright 2021 The Dapr Authors
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

package options

import (
	"flag"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

func TestAppFlag(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

	opts := New([]string{"--app-id", "testapp", "--app-port", "80", "--app-protocol", "http", "--metrics-port", strconv.Itoa(10000)})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, "80", opts.AppPort)
	assert.EqualValues(t, "http", opts.AppProtocol)
}

func TestStandaloneGlobalConfig(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

	opts := New([]string{"--app-id", "testapp", "--mode", string(modes.StandaloneMode), "--config", "../../../pkg/config/testdata/metric_disabled.yaml", "--metrics-port", strconv.Itoa(10000)})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, string(modes.StandaloneMode), opts.Mode)
	assert.Equal(t, "../../../pkg/config/testdata/metric_disabled.yaml", opts.ConfigPath)
}
