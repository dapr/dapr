package errorx

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// StackTraceFilePathTransformer is a  used defined transformer for file path in stack trace output.
type StackTraceFilePathTransformer func(string) string

// InitializeStackTraceTransformer provides a transformer to be used in formatting of all the errors.
// It is OK to leave it alone, stack trace will retain its exact original information.
// This feature may be beneficial, however, if a shortening of file path will make it more convenient to use.
// One of such examples is to transform a project-related path from absolute to relative and thus more IDE-friendly.
//
// NB: error is returned if a transformer was already registered.
// Transformer is changed nonetheless, the old one is returned along with an error.
// User is at liberty to either ignore it, panic, reinstate the old transformer etc.
func InitializeStackTraceTransformer(transformer StackTraceFilePathTransformer) (StackTraceFilePathTransformer, error) {
	stackTraceTransformer.mu.Lock()
	defer stackTraceTransformer.mu.Unlock()

	old := stackTraceTransformer.transform.Load().(StackTraceFilePathTransformer)
	stackTraceTransformer.transform.Store(transformer)

	if stackTraceTransformer.initialized {
		return old, InitializationFailed.New("stack trace transformer was already set up: %#v", old)
	}

	stackTraceTransformer.initialized = true
	return nil, nil
}

var stackTraceTransformer = struct {
	mu          *sync.Mutex
	transform   *atomic.Value
	initialized bool
}{
	&sync.Mutex{},
	&atomic.Value{},
	false,
}

func init() {
	stackTraceTransformer.transform.Store(transformStackTraceLineNoop)
}

var transformStackTraceLineNoop StackTraceFilePathTransformer = func(line string) string {
	return line
}

const (
	stackTraceDepth = 128
	// tuned so that in all control paths of error creation the first frame is useful
	// that is, the frame where New/Wrap/Decorate etc. are called; see TestStackTraceStart
	skippedFrames = 6
)

func collectStackTrace() *stackTrace {
	var pc [stackTraceDepth]uintptr
	depth := runtime.Callers(skippedFrames, pc[:])
	return &stackTrace{
		pc: pc[:depth],
	}
}

type stackTrace struct {
	pc              []uintptr
	causeStackTrace *stackTrace
}

func (st *stackTrace) enhanceWithCause(causeStackTrace *stackTrace) {
	st.causeStackTrace = causeStackTrace
}

func (st *stackTrace) Format(s fmt.State, verb rune) {
	if st == nil {
		return
	}

	switch verb {
	case 'v', 's':
		st.formatStackTrace(s)

		if st.causeStackTrace != nil {
			io.WriteString(s, "\n ---------------------------------- ")
			st.causeStackTrace.Format(s, verb)
		}
	}
}

func (st *stackTrace) formatStackTrace(s fmt.State) {
	transformLine := stackTraceTransformer.transform.Load().(StackTraceFilePathTransformer)

	pc, cropped := st.deduplicateFramesWithCause()
	if len(pc) == 0 {
		return
	}

	frames := frameHelperSingleton.GetFrames(pc)
	for _, frame := range frames {
		io.WriteString(s, "\n at ")
		io.WriteString(s, frame.Function())
		io.WriteString(s, "()\n\t")
		io.WriteString(s, transformLine(frame.File()))
		io.WriteString(s, ":")
		io.WriteString(s, strconv.Itoa(frame.Line()))
	}

	if cropped > 0 {
		io.WriteString(s, "\n ...\n (")
		io.WriteString(s, strconv.Itoa(cropped))
		io.WriteString(s, " duplicated frames)")
	}
}

func (st *stackTrace) deduplicateFramesWithCause() ([]uintptr, int) {
	if st.causeStackTrace == nil {
		return st.pc, 0
	}

	pc := st.pc
	causePC := st.causeStackTrace.pc

	for i := 1; i <= len(pc) && i <= len(causePC); i++ {
		if pc[len(pc)-i] != causePC[len(causePC)-i] {
			return pc[:len(pc)-i], i - 1
		}
	}

	return nil, len(pc)
}
