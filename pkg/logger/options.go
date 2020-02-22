// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package logger

import (
	"fmt"
)

const (
	defaultJSONformat  = false
	defaultOutputLevel = InfoLevel
	defaultOutputSink  = "stdout"
)

var stringToDaprLoggingLevel = map[string]Level{
	"debug": DebugLevel,
	"info":  InfoLevel,
	"warn":  WarnLevel,
	"error": ErrorLevel,
	"fatal": FatalLevel,
}

var daprLoggingLevelToString = map[Level]string{
	DebugLevel: "debug",
	InfoLevel:  "info",
	WarnLevel:  "warn",
	ErrorLevel: "error",
	FatalLevel: "fatal",
}

// Options defines the sets of options for Dapr logging
type Options struct {
	// JSONFormatEnabled is to write log as JSON format
	JSONFormatEnabled bool

	outputLevel string
}

// GetOutputLevel returns the log output level
func (o *Options) GetOutputLevel() Level {
	lvl, ok := stringToDaprLoggingLevel[o.outputLevel]
	// fallback to defaultOutputLevel
	if !ok {
		lvl = defaultOutputLevel
		o.outputLevel = daprLoggingLevelToString[defaultOutputLevel]
	}
	return lvl
}

// SetOutputLevel sets the log output level
func (o *Options) SetOutputLevel(outputLevel Level) error {
	lvl, ok := daprLoggingLevelToString[outputLevel]
	if ok {
		o.outputLevel = lvl
		return nil
	}
	return fmt.Errorf("undefined Log Output Level: %d", outputLevel)
}

// AttachOptionFlags attaches log options to command flags
func (o *Options) AttachOptionFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string)) {

	stringVar(&o.outputLevel, "log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
	boolVar(&o.JSONFormatEnabled, "log-json-enabled", false, "Format log output as JSON or plain-text")
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
