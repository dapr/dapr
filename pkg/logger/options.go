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
	defaultOutputLevel = InfoLevel
	defaultDaprID      = ""
)

var stringToDaprLogLevel = map[string]LogLevel{
	"debug": DebugLevel,
	"info":  InfoLevel,
	"warn":  WarnLevel,
	"error": ErrorLevel,
	"fatal": FatalLevel,
}

var daprLogLevelToString = map[LogLevel]string{
	DebugLevel: "debug",
	InfoLevel:  "info",
	WarnLevel:  "warn",
	ErrorLevel: "error",
	FatalLevel: "fatal",
}

// Options defines the sets of options for Dapr logging
type Options struct {
	// JSONFormatEnabled is the flag to enable JSON formatted log
	JSONFormatEnabled bool

	// daprID is the unique id of Dapr Application
	daprID string

	// outputLevel is the level of logging
	outputLevel string
}

// GetOutputLevel returns the log output level
func (o *Options) GetOutputLevel() LogLevel {
	lvl, ok := stringToDaprLogLevel[o.outputLevel]
	// fallback to defaultOutputLevel
	if !ok {
		lvl = defaultOutputLevel
		o.outputLevel = daprLogLevelToString[defaultOutputLevel]
	}
	return lvl
}

// SetOutputLevel sets the log output level
func (o *Options) SetOutputLevel(outputLevel LogLevel) error {
	lvl, ok := daprLogLevelToString[outputLevel]
	if ok {
		o.outputLevel = lvl
		return nil
	}
	return fmt.Errorf("undefined Log Output Level: %d", outputLevel)
}

// SetDaprID sets Dapr ID
func (o *Options) SetDaprID(id string) {
	o.daprID = id
}

// AttachCmdFlags attaches log options to command flags
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string)) {
	stringVar(
		&o.outputLevel,
		"log-level",
		daprLogLevelToString[defaultOutputLevel],
		"Options are debug, info, warning, error, fatal, or panic. (default info)")
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
		daprID:            defaultDaprID,
		outputLevel:       daprLogLevelToString[defaultOutputLevel],
	}
}

// ApplyOptionsToLoggers applys options to all registered loggers
func ApplyOptionsToLoggers(options *Options) error {
	internalLoggers := getLoggers()

	for _, v := range internalLoggers {
		v.SetOutputLevel(options.GetOutputLevel())
		v.EnableJSONOutput(options.JSONFormatEnabled)
		if options.daprID != defaultDaprID {
			v.SetDaprID(options.daprID)
		}
	}

	return nil
}
