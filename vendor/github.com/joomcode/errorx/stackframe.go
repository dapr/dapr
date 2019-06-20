package errorx

import (
	"runtime"
)

type frame interface {
	Function() string
	File() string
	Line() int
}

type frameHelper struct {
}

var frameHelperSingleton = &frameHelper{}

type defaultFrame struct {
	frame *runtime.Frame
}

func (f *defaultFrame) Function() string {
	return f.frame.Function
}

func (f *defaultFrame) File() string {
	return f.frame.File
}

func (f *defaultFrame) Line() int {
	return f.frame.Line
}

func (c *frameHelper) GetFrames(pcs []uintptr) []frame {
	frames := runtime.CallersFrames(pcs[:])
	result := make([]frame, 0, len(pcs))

	var rawFrame runtime.Frame
	next := true
	for next {
		rawFrame, next = frames.Next()
		frameCopy := rawFrame
		frame := &defaultFrame{&frameCopy}
		result = append(result, frame)
	}

	return result
}
