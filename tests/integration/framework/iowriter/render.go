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
	"os"
	"strconv"
	"strings"

	kitstrings "github.com/dapr/kit/strings"
)

// Field names emitted by dapr/kit/logger. `time` is replaced by the offset the
// line was captured at, the rest carry no information worth a column.
var droppedFields = map[string]bool{
	"time":     true,
	"instance": true,
	"type":     true,
	"ver":      true,
}

// hoistedFields are lifted into the block header when every parsed line in the
// block agrees on their value, rather than repeated on each line.
var hoistedFields = map[string]bool{
	"app_id": true,
}

var rawLogs = kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_LOGS_RAW"))

type kv struct {
	key string
	val string
}

// entry is a parsed dapr log line. Lines which are not dapr log lines (panics,
// stack traces, etcd output, `go build` package lists) have ok false and are
// rendered verbatim.
type entry struct {
	level  string
	scope  string
	msg    string
	hoist  []kv
	extra  []kv
	raw    string
	parsed bool
}

// parseEntry best-effort parses a logfmt line as written by the logrus text
// formatter. A line only counts as a dapr log line if it carries both a level
// and a message.
func parseEntry(raw string) entry {
	e := entry{raw: raw}
	if rawLogs {
		return e
	}

	pairs, ok := parseLogfmt(raw)
	if !ok {
		return e
	}

	var haveLevel, haveMsg bool
	for _, p := range pairs {
		switch {
		case p.key == "level":
			e.level, haveLevel = p.val, true
		case p.key == "msg":
			e.msg, haveMsg = p.val, true
		case p.key == "scope":
			e.scope = p.val
		case droppedFields[p.key]:
		case hoistedFields[p.key]:
			e.hoist = append(e.hoist, p)
		default:
			e.extra = append(e.extra, p)
		}
	}

	if !haveLevel || !haveMsg {
		return entry{raw: raw}
	}

	e.parsed = true
	return e
}

// parseLogfmt scans `key=value` pairs, honouring double quoted values. It
// returns false the moment the input stops looking like logfmt, so that
// arbitrary process output is never mangled.
func parseLogfmt(s string) ([]kv, bool) {
	var pairs []kv

	for i := 0; i < len(s); {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}

		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			return nil, false
		}
		key := s[start:i]
		if !validKey(key) {
			return nil, false
		}
		i++

		var val string
		if i < len(s) && s[i] == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, false
			}
			unquoted, err := strconv.Unquote(s[i : j+1])
			if err != nil {
				return nil, false
			}
			val, i = unquoted, j+1
		} else {
			start = i
			for i < len(s) && s[i] != ' ' {
				i++
			}
			val = s[start:i]
		}

		pairs = append(pairs, kv{key: key, val: val})
	}

	return pairs, len(pairs) > 0
}

func validKey(key string) bool {
	if len(key) == 0 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

const (
	// levelWidth is the width of the level column, sized for the longest level
	// name dapr emits.
	levelWidth = 5

	// maxScopeWidth caps the scope column so that one unusually deep scope
	// cannot push every message in the block off to the right. Scopes longer
	// than this overflow their column rather than widening it.
	maxScopeWidth = 26
)

// normalLevel upper cases the level and shortens the only name logrus emits
// which does not fit the level column.
func normalLevel(level string) string {
	if strings.EqualFold(level, "warning") {
		return "WARN"
	}
	return strings.ToUpper(level)
}

func levelColor(level string) string {
	switch strings.ToLower(level) {
	case "fatal", "panic", "error":
		return ansiRed
	case "warn", "warning":
		return ansiYellow
	case "debug", "trace":
		return ansiDim
	default:
		return ansiCyan
	}
}

// format renders the entry, aligning the scope column to scopeWidth. Unparsed
// lines are indented to the same column so they stay visually attached to the
// surrounding log.
func (e entry) format(scopeWidth int) string {
	if !e.parsed {
		return e.raw
	}

	var sb strings.Builder
	sb.WriteString(colorize(pad(normalLevel(e.level), levelWidth), levelColor(e.level)))
	sb.WriteString("  ")
	if scopeWidth > 0 {
		sb.WriteString(colorize(pad(e.scope, scopeWidth), ansiBlue))
		sb.WriteString("  ")
	}
	sb.WriteString(e.msg)

	for _, p := range e.extra {
		sb.WriteString(" ")
		sb.WriteString(colorize(p.key+"="+quoteIfNeeded(p.val), ansiDim))
	}

	return sb.String()
}

func quoteIfNeeded(val string) string {
	if strings.ContainsAny(val, " \t\"") {
		return strconv.Quote(val)
	}
	return val
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
