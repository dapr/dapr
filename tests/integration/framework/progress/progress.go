/*
Copyright 2026 The Dapr Authors
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

// Package progress draws a live progress line for an integration test run.
//
// It writes to the controlling terminal rather than to stdout. `go test`
// captures the test binary's stdout and stderr, and without -v throws away
// everything a passing test wrote, so a progress line sent there would never be
// seen. Writing to /dev/tty side steps that: the line appears on screen as the
// run proceeds, stays out of the output when it is piped to a file, and does
// not need the run to be verbose.
package progress

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	kitstrings "github.com/dapr/kit/strings"
)

// tick is how often the line is redrawn when no test has finished, so that a
// slow test still looks like progress rather than a hang.
const tick = time.Second / 4

const defaultWidth = 80

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Reporter renders the progress of a run. The zero value, and any Reporter
// created when there is no terminal to draw on, is a no-op.
type Reporter struct {
	out    io.Writer
	closer io.Closer
	total  int
	start  time.Time
	width  int

	lock    sync.Mutex
	done    int
	failed  int
	last    string
	frame   int
	lastLen int

	stop chan struct{}
	wg   sync.WaitGroup
}

// New returns a Reporter drawing on the controlling terminal, or a no-op
// Reporter when there is not one to draw on.
func New(total int) *Reporter {
	if !enabled() || total == 0 {
		return new(Reporter)
	}

	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return new(Reporter)
	}

	r := newReporter(tty, total)
	r.closer = tty
	r.run()

	return r
}

// enabled reports whether a progress line should be drawn. A verbose run
// already prints a line per test, so a progress line would be redundant and
// would interleave with it. CI has no terminal worth drawing on, and Windows
// has no /dev/tty.
func enabled() bool {
	if v, ok := os.LookupEnv("DAPR_INTEGRATION_PROGRESS"); ok {
		return kitstrings.IsTruthy(v)
	}
	return runtime.GOOS != "windows" &&
		os.Getenv("GITHUB_ACTIONS") != "true" &&
		!testing.Verbose()
}

func newReporter(out io.Writer, total int) *Reporter {
	return &Reporter{
		out:   out,
		total: total,
		start: time.Now(),
		width: width(),
		stop:  make(chan struct{}),
	}
}

func width() int {
	cols, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || cols < 40 {
		return defaultWidth
	}
	return cols
}

// run redraws the line on a timer so the elapsed time and spinner keep moving
// between test completions.
func (r *Reporter) run() {
	r.wg.Go(func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.lock.Lock()
				r.frame++
				r.draw()
				r.lock.Unlock()
			}
		}
	})
}

// Done records that a test case finished. A failure is also written out as a
// line of its own, so that failures accumulate on screen during a long run
// rather than only appearing at the end.
func (r *Reporter) Done(name string, failed bool) {
	if r.out == nil {
		return
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	r.done++
	r.last = name
	if failed {
		r.failed++
		r.erase()
		fmt.Fprintf(r.out, "  FAIL  %s\n", name)
	}

	r.draw()
}

// Finish removes the progress line, leaving the terminal as it found it.
func (r *Reporter) Finish() {
	if r.out == nil {
		return
	}

	close(r.stop)
	r.wg.Wait()

	r.lock.Lock()
	defer r.lock.Unlock()

	r.erase()
	if r.closer != nil {
		r.closer.Close()
	}
	r.out = nil
}

// erase clears the current line. Expects the lock to be held.
func (r *Reporter) erase() {
	if r.lastLen == 0 {
		return
	}
	fmt.Fprintf(r.out, "\r%s\r", strings.Repeat(" ", r.lastLen))
	r.lastLen = 0
}

// draw rewrites the progress line in place. Expects the lock to be held.
func (r *Reporter) draw() {
	line := r.line()
	fmt.Fprintf(r.out, "\r%s", line)

	// Blank whatever the previous, longer line left behind.
	if pad := r.lastLen - len([]rune(line)); pad > 0 {
		fmt.Fprintf(r.out, "%s\r%s", strings.Repeat(" ", pad), line)
	}

	r.lastLen = len([]rune(line))
}

func (r *Reporter) line() string {
	head := fmt.Sprintf(" %s %d/%d  ok %d",
		spinner[r.frame%len(spinner)], r.done, r.total, r.done-r.failed,
	)
	if r.failed > 0 {
		head += fmt.Sprintf("  fail %d", r.failed)
	}
	head += "  " + elapsed(time.Since(r.start)) + "  "

	// The test name gets whatever room is left, so the line never wraps and
	// leaves an orphaned half line behind it.
	room := r.width - len([]rune(head)) - 1
	if room < 1 {
		return head
	}

	return head + truncate(r.last, room)
}

func elapsed(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// truncate keeps the tail of a test name, which is the part that distinguishes
// it from its siblings.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[len(runes)-max:])
	}
	return "…" + string(runes[len(runes)-max+1:])
}
