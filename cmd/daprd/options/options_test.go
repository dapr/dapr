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
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"

	"github.com/dapr/dapr/pkg/modes"
)

func TestAppFlag(t *testing.T) {
	opts := New([]string{
		"-app-id", "testapp", // Single dash
		"--app-port", "80",
		"--app-protocol", "http",
		"--metrics-port", strconv.Itoa(10000),
	})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, "80", opts.AppPort)
	assert.EqualValues(t, "http", opts.AppProtocol)
}

func TestStandaloneGlobalConfig(t *testing.T) {
	opts := New([]string{
		"--app-id", "testapp",
		"-mode", string(modes.StandaloneMode), // Single dash
		"--config", "../../../pkg/config/testdata/metric_disabled.yaml",
		"--metrics-port", strconv.Itoa(10000),
	})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, string(modes.StandaloneMode), opts.Mode)
	assert.Equal(t, []string{"../../../pkg/config/testdata/metric_disabled.yaml"}, []string(opts.Config))
}

func TestEnableAPILogging(t *testing.T) {
	t.Run("explicitly enabled", func(t *testing.T) {
		opts := New([]string{
			"-enable-api-logging", // Single dash
		})
		require.NotNil(t, opts.EnableAPILogging)
		assert.True(t, *opts.EnableAPILogging)
	})
	t.Run("explicitly enabled with true written out", func(t *testing.T) {
		opts := New([]string{
			"--enable-api-logging=true",
		})
		require.NotNil(t, opts.EnableAPILogging)
		assert.True(t, *opts.EnableAPILogging)
	})

	t.Run("explicitly disabled", func(t *testing.T) {
		opts := New([]string{
			"-enable-api-logging=false", // Single dash
		})
		require.NotNil(t, opts.EnableAPILogging)
		assert.False(t, *opts.EnableAPILogging)
	})

	t.Run("flag is unset", func(t *testing.T) {
		opts := New([]string{})
		require.Nil(t, opts.EnableAPILogging)
	})
}

func TestMultipleConfig(t *testing.T) {
	t.Run("config flag not defined", func(t *testing.T) {
		opts := New([]string{})
		require.Empty(t, opts.Config)
	})

	t.Run("single config", func(t *testing.T) {
		opts := New([]string{
			"--config", "cfg1.yaml",
		})
		require.Equal(t, []string{"cfg1.yaml"}, opts.Config)
	})

	t.Run("comma-separated configs", func(t *testing.T) {
		opts := New([]string{
			"-config=cfg1.yaml,cfg2.yaml", // Single dash
		})
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml"}, opts.Config)
	})

	t.Run("multiple config flags", func(t *testing.T) {
		opts := New([]string{
			"-config=cfg1.yaml",    // Single dash
			"-config", "cfg2.yaml", // Single dash
		})
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml"}, opts.Config)
	})

	t.Run("multiple config flags and comma-separated values", func(t *testing.T) {
		opts := New([]string{
			"-config=cfg1.yaml", // Single dash
			"--config", "cfg2.yaml,cfg3.yaml",
		})
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml", "cfg3.yaml"}, opts.Config)
	})
}

func TestUnknownFlags(t *testing.T) {
	// Change os.Stdout temporarily to capture logs
	prevStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Get all logs in a buffer
	buf := &bytes.Buffer{}
	doneCh := make(chan struct{})
	go func() {
		io.Copy(buf, r)
		close(doneCh)
	}()

	// Parse flags
	opts := New([]string{
		"--app-id", "testapp",
		"--foo1",        // Two dashes, no value
		"--foo2", "bar", // Two dashes, value as separate item
		"--foo3=bar",   // Two dashes, value after =
		"-foo4",        // Single dash, no value
		"-foo5", "bar", // Single dash, value as separate item
		"-foo6=bar", // Single dash, value after =
	})

	// Reset stdout and close the pipe
	os.Stdout = prevStdout
	w.Close()

	// Wait for all data to be read from the pipe
	<-doneCh

	// Check that all logs were sent
	logs := strings.Split(buf.String(), "\n")
	slices.Sort(logs)
	var n int
	for _, v := range logs {
		if strings.HasPrefix(v, "WARN:") {
			logs[n] = v
			n++
		}
	}
	logs = logs[:n]

	expect := []string{
		"WARN: flag --foo1 does not exist and will be ignored",
		"WARN: flag --foo2 does not exist and will be ignored",
		"WARN: flag --foo3 does not exist and will be ignored",
		"WARN: flag --foo4 does not exist and will be ignored",
		"WARN: flag --foo5 does not exist and will be ignored",
		"WARN: flag --foo6 does not exist and will be ignored",
	}
	assert.Equal(t, expect, logs)

	require.Equal(t, "testapp", opts.AppID)
}
