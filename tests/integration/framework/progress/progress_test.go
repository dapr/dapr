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

package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// lines splits terminal output on the carriage returns the reporter uses to
// rewrite in place, so that each redraw can be inspected on its own.
func lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\r") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, " "))
		}
	}
	return out
}

func TestReporter(t *testing.T) {
	t.Run("should be a no-op when there is no terminal", func(t *testing.T) {
		var r Reporter
		r.Done("a/b", false)
		r.Finish()
	})

	t.Run("should count completions", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 3)

		r.Done("ports/daprd", false)
		r.Done("ports/sentry", false)

		got := lines(buf.String())
		assert.Contains(t, got[len(got)-1], "2/3")
		assert.Contains(t, got[len(got)-1], "ok 2")
		assert.Contains(t, got[len(got)-1], "ports/sentry")
	})

	t.Run("should show the name of the most recent test", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 2)

		r.Done("actors/reminders/period", false)
		assert.Contains(t, buf.String(), "actors/reminders/period")
	})

	t.Run("should report a failure on its own line and keep a count", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 2)

		r.Done("ports/daprd", true)
		r.Done("ports/sentry", false)

		assert.Contains(t, buf.String(), "FAIL  ports/daprd\n")

		got := lines(buf.String())
		assert.Contains(t, got[len(got)-1], "fail 1")
		assert.Contains(t, got[len(got)-1], "ok 1")
	})

	t.Run("should not mention failures when there are none", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 1)

		r.Done("ports/daprd", false)
		assert.NotContains(t, buf.String(), "fail")
	})

	t.Run("should erase the line on finish", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 1)
		r.Done("ports/daprd", false)
		r.Finish()

		assert.True(t, strings.HasSuffix(buf.String(), "\r"), "line should be blanked")
		assert.Nil(t, r.out, "further calls should be no-ops")

		r.Done("ports/sentry", false)
	})

	t.Run("should keep the line within the terminal width", func(t *testing.T) {
		t.Setenv("COLUMNS", "60")

		var buf bytes.Buffer
		r := newReporter(&buf, 1)
		r.Done(strings.Repeat("very/long/name/", 20), false)

		for _, l := range lines(buf.String()) {
			assert.LessOrEqual(t, len([]rune(l)), 60, l)
		}
	})

	t.Run("should survive concurrent completions", func(t *testing.T) {
		var buf bytes.Buffer
		r := newReporter(&buf, 100)
		r.run()

		var wg sync.WaitGroup
		wg.Add(100)
		for i := range 100 {
			go func() {
				defer wg.Done()
				r.Done("case", i%10 == 0)
			}()
		}
		wg.Wait()
		r.Finish()

		assert.Equal(t, 100, r.done)
		assert.Equal(t, 10, r.failed)
	})
}

func TestElapsed(t *testing.T) {
	tests := map[time.Duration]string{
		0:                                     "0s",
		1500 * time.Millisecond:               "1s",
		59 * time.Second:                      "59s",
		time.Minute:                           "1m00s",
		3*time.Minute + 25*time.Second:        "3m25s",
		62*time.Minute + 500*time.Millisecond: "62m00s",
	}

	for in, exp := range tests {
		assert.Equal(t, exp, elapsed(in), in.String())
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abc", truncate("abc", 3), "exact fit is untouched")
	assert.Equal(t, "…c", truncate("abc", 2))
	assert.Equal(t, "c", truncate("abc", 1))
	// The tail is what distinguishes sibling test names.
	assert.Equal(t, "…reminders/period", truncate("actors/reminders/period", 17))
}
