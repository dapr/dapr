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
)

var stringToDaprLoggingLevel = map[string]LogLevel{
	"debug": DebugLevel,
	"info":  InfoLevel,
	"warn":  WarnLevel,
	"error": ErrorLevel,
	"fatal": FatalLevel,
}

var daprLoggingLevelToString = map[LogLevel]string{
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

	// outputLevel is the level of logging
	outputLevel string
}

// GetOutputLevel returns the log output level
func (o *Options) GetOutputLevel() LogLevel {
	lvl, ok := stringToDaprLoggingLevel[o.outputLevel]
	// fallback to defaultOutputLevel
	if !ok {
		lvl = defaultOutputLevel
		o.outputLevel = daprLoggingLevelToString[defaultOutputLevel]
	}
	return lvl
}

// SetOutputLevel sets the log output level
func (o *Options) SetOutputLevel(outputLevel LogLevel) error {
	lvl, ok := daprLoggingLevelToString[outputLevel]
	if ok {
		o.outputLevel = lvl
		return nil
	}
	return fmt.Errorf("undefined Log Output Level: %d", outputLevel)
}

// AttachCmdFlags attaches log options to command flags
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string)) {
	stringVar(
		&o.outputLevel,
		"log-level",
		daprLoggingLevelToString[defaultOutputLevel],
		"Options are debug, info, warning, error, fatal, or panic. (default info)")
	boolVar(
		&o.JSONFormatEnabled,
		"log-json-enabled",
		defaultJSONOutput,
		"Format log output as JSON or plain-text")
}

// ApplyOptionsToLoggers applys options to all registered loggers
func ApplyOptionsToLoggers(options *Options) error {
	internalLoggers := getLoggers()

	for _, v := range internalLoggers {
		v.SetOutputLevel(options.GetOutputLevel())
		v.EnableJSONOutput(options.JSONFormatEnabled)
	}

	return nil
}
