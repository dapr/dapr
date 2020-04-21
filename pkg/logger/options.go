// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package logger

import "fmt"

const (
	defaultJSONOutput  = false
	defaultOutputLevel = "info"
	undefinedAppID     = ""
)

// Options defines the sets of options for Dapr logging
type Options struct {
	// appID is the unique id of Dapr Application
	appID string

	// JSONFormatEnabled is the flag to enable JSON formatted log
	JSONFormatEnabled bool

	// OutputLevel is the level of logging
	OutputLevel string
}

// SetOutputLevel sets the log output level
func (o *Options) SetOutputLevel(outputLevel string) error {
	if toLogLevel(outputLevel) == UndefinedLevel {
		return fmt.Errorf("undefined Log Output Level:%s", outputLevel)
	}
	o.OutputLevel = outputLevel
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
		&o.OutputLevel,
		"log-level",
		defaultOutputLevel,
		"Options are debug, info, warning, error, or fatal")
	boolVar(
		&o.JSONFormatEnabled,
		"log-as-json",
		defaultJSONOutput,
		"print log as JSON (default false)")
}

// DefaultOptions returns default values of Options
func DefaultOptions() Options {
	return Options{
		JSONFormatEnabled: defaultJSONOutput,
		appID:             undefinedAppID,
		OutputLevel:       defaultOutputLevel,
	}
}

// ApplyOptionsToLoggers applys options to all registered loggers
func ApplyOptionsToLoggers(options *Options) error {
	internalLoggers := getLoggers()

	// Apply formatting options first
	for _, v := range internalLoggers {
		v.EnableJSONOutput(options.JSONFormatEnabled)

		if options.appID != undefinedAppID {
			v.SetAppID(options.appID)
		}
	}

	daprLogLevel := toLogLevel(options.OutputLevel)
	if daprLogLevel == UndefinedLevel {
		return fmt.Errorf("invalid value for --log-level: %s", options.OutputLevel)
	}

	for _, v := range internalLoggers {
		v.SetOutputLevel(daprLogLevel)
	}
	return nil
}
