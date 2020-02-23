// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		o := DefaultOptions()
		assert.Equal(t, defaultJSONOutput, o.JSONFormatEnabled)
		assert.Equal(t, undefinedDaprID, o.daprID)
		assert.Equal(t, defaultOutputLevel, o.GetOutputLevel())
	})

	t.Run("set outputlevel to debug level", func(t *testing.T) {
		o := DefaultOptions()
		assert.Equal(t, defaultOutputLevel, o.GetOutputLevel())

		o.SetOutputLevel(DebugLevel)
		assert.Equal(t, DebugLevel, o.GetOutputLevel())
	})

	t.Run("set dapr ID", func(t *testing.T) {
		o := DefaultOptions()
		assert.Equal(t, undefinedDaprID, o.daprID)

		o.SetDaprID("dapr-app")
		assert.Equal(t, "dapr-app", o.daprID)
	})

	t.Run("attaching log related cmd flags", func(t *testing.T) {
		o := DefaultOptions()

		logLevelAsserted := false
		testStringVarFn := func(p *string, name string, value string, usage string) {
			if name == "log-level" && value == daprLogLevelToString[defaultOutputLevel] {
				logLevelAsserted = true
			}
		}

		logJSONEnabledAsserted := false
		testBoolVarFn := func(p *bool, name string, value bool, usage string) {
			if name == "log-json-enabled" && value == defaultJSONOutput {
				logJSONEnabledAsserted = true
			}
		}

		o.AttachCmdFlags(testStringVarFn, testBoolVarFn)

		// assert
		assert.True(t, logLevelAsserted)
		assert.True(t, logJSONEnabledAsserted)
	})
}

func TestApplyOptionsToLoggers(t *testing.T) {
	testOptions := Options{
		JSONFormatEnabled: true,
		daprID:            "dapr-app",
		outputLevel:       "debug",
	}

	// Create two loggers
	var testLoggers = []Logger{
		NewLogger("testLogger0"),
		NewLogger("testLogger1"),
	}

	for _, l := range testLoggers {
		l.EnableJSONOutput(false)
		l.SetOutputLevel(InfoLevel)
	}

	ApplyOptionsToLoggers(&testOptions)

	for _, l := range testLoggers {
		assert.Equal(
			t,
			"dapr-app",
			(l.(*daprLogger)).logger.Data[logFieldDaprID])
		assert.Equal(
			t,
			daprLogLevelToLogrusLevel[DebugLevel],
			(l.(*daprLogger)).logger.Logger.GetLevel())
	}
}
