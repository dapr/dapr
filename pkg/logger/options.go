// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package logger

import (
	"fmt"
)

const (
	defaultJSONOutput  = false
	defaultOutputLevel = "info"
	undefinedAppID     = ""
)

// Options defines the sets of options for Dapr logging
type Options struct {
	// JSONFormatEnabled is the flag to enable JSON formatted log
	JSONFormatEnabled bool

	// appID is the unique id of Dapr Application
	appID string

	// outputLevel is the level of logging
	outputLevel string
}

// SetOutputLevel sets the log output level
func (o *Options) SetOutputLevel(outputLevel string) error {
	if toLogLevel(outputLevel) == UndefinedLevel {
		return fmt.Errorf("undefined Log Output Level:%s", outputLevel)
	}
	o.outputLevel = outputLevel
	return nil
}

// SetAppID sets Dapr ID
func (o *Options) SetAppID(id string) {
	o.appID = id
}

// AttachCmdFlags attaches log options to command flags
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string)) {
	stringVar(
		&o.outputLevel,
		"log-level",
		defaultOutputLevel,
		"Options are debug, info, warning, error, or fatal. (default info)")
	boolVar(
		&o.JSONFormatEnabled,
		"log-json-enabled",
		defaultJSONOutput,
		"Format log output as JSON or plain-text")
}

// DefaultOptions returns default values of Options
func DefaultOptions() Options {
	return Options{
		JSONFormatEnabled: defaultJSONOutput,
		appID:             undefinedAppID,
		outputLevel:       defaultOutputLevel,
	}
}

// ApplyOptionsToLoggers applys options to all registered loggers
func ApplyOptionsToLoggers(options *Options) error {
	internalLoggers := getLoggers()

	// Apply formatting options first
	for _, v := range internalLoggers {
		if err := v.EnableJSONOutput(options.JSONFormatEnabled); err != nil {
			return err
		}

		if options.appID != undefinedAppID {
			v.SetAppID(options.appID)
		}
	}

	daprLogLevel := toLogLevel(options.outputLevel)
	if daprLogLevel == UndefinedLevel {
		return fmt.Errorf("invalid value for --log-level: %s", options.outputLevel)
	}

	for _, v := range internalLoggers {
		if err := v.SetOutputLevel(daprLogLevel); err != nil {
			return err
		}
	}

	return nil
}
