package fasthttpcors

import (
	"fmt"
	"io"
)

type Logger interface {
	Log(...interface{})
}

type logger struct {
	writer io.Writer
}

type offlogger struct{}

func (t *logger) Log(a ...interface{}) {
	t.writer.Write([]byte(fmt.Sprint(a...)))
	t.writer.Write([]byte("\n"))
}

func (t *offlogger) Log(a ...interface{}) {}

// NewLogger return normal tracer
func NewLogger(w io.Writer) Logger {
	return &logger{writer: w}
}

// OffLogger return tracer that do nothing
func OffLogger() Logger {
	return &offlogger{}
}
