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

package iowriter

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitstrings "github.com/dapr/kit/strings"
)

// eventsBlock is the name of the block holding framework events, i.e. what the
// test harness did rather than what a process printed.
const eventsBlock = "framework"

// bannerWidth is the width the log banner is padded to.
const bannerWidth = 78

// collectors holds the collector for each test. Entries outlive their test so
// that late output from a process finds a collector to discard it, but the
// captured lines themselves are released once the test has reported.
var collectors sync.Map

// streaming reports whether lines should be written to stderr as they arrive
// rather than buffered until the test finishes. Only useful when focused on a
// single test, since parallel tests interleave.
func streaming() bool {
	return strings.EqualFold(os.Getenv("DAPR_INTEGRATION_LOGS"), "stream")
}

// alwaysLog reports whether logs should be emitted even when the test passed.
func alwaysLog() bool {
	return kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_LOGS"))
}

type line struct {
	at   time.Duration
	text string
}

// block is the captured output of a single source: one process, or the
// framework itself.
type block struct {
	c    *collector
	name string

	lock  sync.Mutex
	lines []line
}

func (b *block) append(at time.Duration, text string) {
	// A process can outlive the test which started it. Dropping its late output
	// is far better than the panic a t.Log after test completion would cause.
	if b.c != nil && b.c.finished() {
		return
	}

	// Streamed lines carry the test they belong to, since tests running in
	// parallel share stderr.
	if streaming() {
		fmt.Fprintf(os.Stderr, "%s %s %s %s\n",
			offset(at), b.c.shortName(), pad(b.name, 10),
			parseEntry(text).format(maxScopeWidth),
		)
		return
	}

	b.lock.Lock()
	defer b.lock.Unlock()
	b.lines = append(b.lines, line{at: at, text: text})
}

// collector gathers every block belonging to a single test, and emits them as
// one pretty printed report if the test fails.
type collector struct {
	t  Logger
	t0 time.Time

	lock    sync.Mutex
	events  *block
	blocks  []*block
	names   map[string]int
	writers []*stdwriter
	notes   []string
	done    atomic.Bool
}

// finished reports whether the report has already been emitted, after which
// further output is dropped rather than recorded.
func (c *collector) finished() bool {
	return c.done.Load()
}

// collectorFor returns the collector for t, creating it on first use. Creation
// registers the flush cleanup, which is why it must happen before any process
// registers its own cleanup: cleanups run last in, first out, so the flush runs
// after every process has exited and drained its output.
func collectorFor(t Logger) *collector {
	if c, ok := collectors.Load(t); ok {
		return c.(*collector)
	}

	c := &collector{
		t:     t,
		t0:    time.Now(),
		names: make(map[string]int),
	}
	c.events = &block{c: c, name: eventsBlock}

	actual, loaded := collectors.LoadOrStore(t, c)
	if loaded {
		return actual.(*collector)
	}

	t.Cleanup(c.flush)

	return c
}

func (c *collector) since() time.Duration {
	return time.Since(c.t0)
}

// shortName is the test name without the suite prefix every case shares.
func (c *collector) shortName() string {
	return strings.TrimPrefix(c.t.Name(), "Test_Integration/")
}

// newBlock registers a source with the collector. Repeated names are
// suffixed with an index so that, say, three daprds in one test are
// distinguishable as daprd, daprd-1 and daprd-2.
func (c *collector) newBlock(name string) *block {
	c.lock.Lock()
	defer c.lock.Unlock()

	n := c.names[name]
	c.names[name]++
	if n > 0 {
		name += "-" + strconv.Itoa(n)
	}

	b := &block{c: c, name: name}
	c.blocks = append(c.blocks, b)

	return b
}

func (c *collector) addWriter(w *stdwriter) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.writers = append(c.writers, w)
}

// note records a message shown in the report banner, for conditions which
// explain the whole failure such as a blown deadline.
func (c *collector) note(msg string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.notes = append(c.notes, msg)
}

func (c *collector) flush() {
	c.lock.Lock()
	writers := make([]*stdwriter, len(c.writers))
	copy(writers, c.writers)
	c.lock.Unlock()

	// Move any trailing partial line into its block. Processes which exited
	// without a final newline would otherwise lose their last, often most
	// interesting, line.
	for _, w := range writers {
		w.Close()
	}

	if !streaming() && (c.t.Failed() || alwaysLog()) {
		c.report()
	}

	// The entry is kept so that a process outliving its test finds a collector
	// which drops its output, rather than one which registers a cleanup on an
	// already finished test. Only the captured lines are released.
	c.done.Store(true)
	c.release()
}

func (c *collector) release() {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, b := range append([]*block{c.events}, c.blocks...) {
		b.lock.Lock()
		b.lines = nil
		b.lock.Unlock()
	}
	c.blocks = nil
	c.writers = nil
	c.notes = nil
}

// report writes the collected logs to their own file and leaves a single line
// in the test output pointing at it. A full suite run produces tens of
// thousands of log lines, which drown the failures they are meant to explain
// when written to the terminal.
func (c *collector) report() {
	var sb strings.Builder
	c.render(&sb)
	if sb.Len() == 0 {
		return
	}

	if inline() {
		fmt.Fprint(c.t.Output(), sb.String())
		if c.t.Failed() {
			recordFailure(c.shortName(), "")
		}
		return
	}

	path, err := writeReport(c.shortName(), sb.String())
	if err != nil {
		// Falling back to the test output is noisy, but losing the logs
		// entirely would be worse.
		fmt.Fprintf(c.t.Output(), "could not write log file (%v), falling back to inline logs\n", err)
		fmt.Fprint(c.t.Output(), sb.String())
		return
	}

	fmt.Fprintf(c.t.Output(), "logs: %s\n", path)
	if c.t.Failed() {
		recordFailure(c.shortName(), path)
	}
}

func (c *collector) render(w io.Writer) {
	c.lock.Lock()
	blocks := append([]*block{c.events}, c.blocks...)
	notes := make([]string, len(c.notes))
	copy(notes, c.notes)
	c.lock.Unlock()

	var body strings.Builder
	for _, note := range notes {
		body.WriteString("  " + colorize(note, ansiRed, ansiBold) + "\n")
	}
	for _, b := range blocks {
		b.render(&body)
	}

	// An empty report is worse than none: it is two lines of banner saying
	// nothing happened.
	if body.Len() == 0 {
		return
	}

	fmt.Fprint(w, c.banner()+body.String()+colorize(rule(bannerWidth), ansiDim)+"\n")
}

func (c *collector) banner() string {
	status, color := "PASS", ansiGreen
	if c.t.Failed() {
		status, color = "FAIL", ansiRed
	}

	head := fmt.Sprintf("%s logs: %s (%s after %s) ",
		rule(4), c.shortName(), colorize(status, color, ansiBold), c.since().Truncate(time.Millisecond*10),
	)

	// Pad to the banner width, accounting for the invisible escapes the status
	// colouring added.
	visible := len([]rune(head)) - len([]rune(colorize(status, color, ansiBold))) + len(status)
	if visible < bannerWidth {
		head += rule(bannerWidth - visible)
	}

	return colorize(head, ansiDim) + "\n"
}

func (b *block) render(sb *strings.Builder) {
	b.lock.Lock()
	lines := make([]line, len(b.lines))
	copy(lines, b.lines)
	b.lock.Unlock()

	if len(lines) == 0 {
		return
	}

	entries := make([]entry, len(lines))
	var scopeWidth int
	for i, l := range lines {
		entries[i] = parseEntry(l.text)
		if w := len(entries[i].scope); w > scopeWidth {
			scopeWidth = min(w, maxScopeWidth)
		}
	}

	header, lifted := b.header(entries)

	// Anything not lifted into the header stays on the line it came from.
	for i, e := range entries {
		for _, p := range e.hoist {
			if !lifted[p.key] {
				entries[i].extra = append(entries[i].extra, p)
			}
		}
	}

	sb.WriteString(header)
	for i, e := range entries {
		sb.WriteString("  " + colorize(offset(lines[i].at), ansiDim) + "  " + e.format(scopeWidth) + "\n")
	}
}

// header names the block, lifting any field which is constant across the whole
// block (such as the app ID of a daprd) out of the per line output. It returns
// the set of fields it lifted.
func (b *block) header(entries []entry) (string, map[string]bool) {
	var parsed int
	vals := make(map[string]string)
	counts := make(map[string]int)
	conflict := make(map[string]bool)

	for _, e := range entries {
		if !e.parsed {
			continue
		}
		parsed++
		for _, p := range e.hoist {
			if prev, ok := vals[p.key]; ok && prev != p.val {
				conflict[p.key] = true
			}
			vals[p.key] = p.val
			counts[p.key]++
		}
	}

	// Only lift a field out of the lines if every parsed line carries it with
	// the same value, otherwise dropping it from the lines would lose data.
	lifted := make(map[string]bool, len(vals))
	keys := make([]string, 0, len(vals))
	for key := range vals {
		if !conflict[key] && counts[key] == parsed {
			lifted[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var head strings.Builder
	head.WriteString(rule(2) + " " + b.name)
	for _, key := range keys {
		head.WriteString(" " + key + "=" + vals[key])
	}
	head.WriteString(" " + rule(2))

	return colorize(head.String(), ansiDim, ansiBold) + "\n", lifted
}

func offset(d time.Duration) string {
	return fmt.Sprintf("%7.3fs", d.Seconds())
}
