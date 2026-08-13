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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLogfmt(t *testing.T) {
	tests := map[string]struct {
		in    string
		exp   []kv
		expOK bool
	}{
		"empty": {
			in: "", expOK: false,
		},
		"bare words": {
			in: "panic: runtime error", expOK: false,
		},
		"stack trace": {
			in: "\tgithub.com/dapr/dapr/pkg/runtime.(*Runtime).Run(0x1234)", expOK: false,
		},
		"unquoted pairs": {
			in:    "level=info scope=dapr.runtime",
			exp:   []kv{{"level", "info"}, {"scope", "dapr.runtime"}},
			expOK: true,
		},
		"quoted value with spaces": {
			in:    `level=info msg="dapr initialized. Status: Running"`,
			exp:   []kv{{"level", "info"}, {"msg", "dapr initialized. Status: Running"}},
			expOK: true,
		},
		"escaped quote in value": {
			in:    `msg="he said \"hi\"" level=warning`,
			exp:   []kv{{"msg", `he said "hi"`}, {"level", "warning"}},
			expOK: true,
		},
		"empty value": {
			in:    "app_id= level=info",
			exp:   []kv{{"app_id", ""}, {"level", "info"}},
			expOK: true,
		},
		"unterminated quote": {
			in: `msg="never closed`, expOK: false,
		},
		"missing equals": {
			in: "level=info orphan", expOK: false,
		},
		"invalid key": {
			in: "not a key=value", expOK: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := parseLogfmt(test.in)
			assert.Equal(t, test.expOK, ok)
			if test.expOK {
				assert.Equal(t, test.exp, got)
			}
		})
	}
}

func TestParseEntry(t *testing.T) {
	const daprLine = `time="2026-08-13T12:00:00.123456789Z" level=info ` +
		`msg="dapr initialized. Status: Running" app_id=myapp instance=host ` +
		`scope=dapr.runtime type=log ver=edge`

	t.Run("should parse a dapr log line", func(t *testing.T) {
		e := parseEntry(daprLine)
		assert.True(t, e.parsed)
		assert.Equal(t, "info", e.level)
		assert.Equal(t, "dapr.runtime", e.scope)
		assert.Equal(t, "dapr initialized. Status: Running", e.msg)
		assert.Equal(t, []kv{{"app_id", "myapp"}}, e.hoist)
		assert.Empty(t, e.extra)
	})

	t.Run("should keep unrecognised fields", func(t *testing.T) {
		e := parseEntry(`level=error msg=boom component=statestore attempt=3`)
		assert.True(t, e.parsed)
		assert.Equal(t, []kv{{"component", "statestore"}, {"attempt", "3"}}, e.extra)
	})

	t.Run("should not parse without a level and message", func(t *testing.T) {
		for _, line := range []string{
			`level=info scope=dapr.runtime`,
			`msg="no level here"`,
			`{"level":"info","msg":"json not logfmt"}`,
			`panic: close of closed channel`,
		} {
			e := parseEntry(line)
			assert.False(t, e.parsed, line)
			assert.Equal(t, line, e.raw)
		}
	})

	t.Run("should not parse when raw logs are requested", func(t *testing.T) {
		rawLogs = true
		t.Cleanup(func() { rawLogs = false })

		e := parseEntry(daprLine)
		assert.False(t, e.parsed)
		assert.Equal(t, daprLine, e.raw)
	})
}

func TestEntryFormat(t *testing.T) {
	t.Run("should render level, scope and message", func(t *testing.T) {
		e := parseEntry(`level=info msg="all good" scope=dapr.runtime`)
		assert.Equal(t, "INFO   dapr.runtime  all good", e.format(len("dapr.runtime")))
	})

	t.Run("should align scopes across a block", func(t *testing.T) {
		short := parseEntry(`level=error msg=boom scope=dapr.api`)
		assert.Equal(t, "ERROR  dapr.api      boom", short.format(len("dapr.runtime")))
	})

	t.Run("should append extra fields", func(t *testing.T) {
		e := parseEntry(`level=warning msg=slow scope=dapr.api took="1.5 s"`)
		assert.Equal(t, `WARN   dapr.api  slow took="1.5 s"`, e.format(len("dapr.api")))
	})

	t.Run("should pass unparsed lines through verbatim", func(t *testing.T) {
		const raw = "goroutine 1 [running]:"
		assert.Equal(t, raw, parseEntry(raw).format(12))
	})
}

func TestBlockHeader(t *testing.T) {
	newBlockWith := func(t *testing.T, lines ...string) (*block, []entry) {
		t.Helper()
		b := &block{name: "daprd"}
		entries := make([]entry, len(lines))
		for i, l := range lines {
			entries[i] = parseEntry(l)
		}
		return b, entries
	}

	t.Run("should hoist a field constant across the block", func(t *testing.T) {
		b, entries := newBlockWith(t,
			`level=info msg=one app_id=myapp`,
			`level=info msg=two app_id=myapp`,
		)
		header, lifted := b.header(entries)
		assert.Equal(t, "-- daprd app_id=myapp --\n", header)
		assert.True(t, lifted["app_id"])
	})

	t.Run("should not hoist a field which varies", func(t *testing.T) {
		b, entries := newBlockWith(t,
			`level=info msg=one app_id=a`,
			`level=info msg=two app_id=b`,
		)
		header, lifted := b.header(entries)
		assert.Equal(t, "-- daprd --\n", header)
		assert.False(t, lifted["app_id"])
	})

	t.Run("should not hoist a field missing from some lines", func(t *testing.T) {
		b, entries := newBlockWith(t,
			`level=info msg=one app_id=a`,
			`level=info msg=two`,
		)
		header, lifted := b.header(entries)
		assert.Equal(t, "-- daprd --\n", header)
		assert.False(t, lifted["app_id"])
	})
}

func TestBlockRender(t *testing.T) {
	t.Run("should keep unhoisted fields on their line", func(t *testing.T) {
		c := &collector{}
		b := &block{c: c, name: "daprd"}
		b.append(0, `level=info msg=one app_id=a`)
		b.append(0, `level=info msg=two app_id=b`)

		var sb strings.Builder
		b.render(&sb)

		out := sb.String()
		assert.Contains(t, out, "app_id=a")
		assert.Contains(t, out, "app_id=b")
	})

	t.Run("should prefix each line with its offset", func(t *testing.T) {
		c := &collector{}
		b := &block{c: c, name: "proc"}
		b.append(0, "first")
		b.append(1500000000, "second")

		var sb strings.Builder
		b.render(&sb)

		assert.Contains(t, sb.String(), fmt.Sprintf("  %s  first\n", offset(0)))
		assert.Contains(t, sb.String(), "  1.500s  second\n")
	})
}
