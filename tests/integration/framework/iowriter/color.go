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
	"runtime"
	"strings"

	kitstrings "github.com/dapr/kit/strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
)

var (
	useColor   = detectColor()
	useUnicode = runtime.GOOS != "windows"
)

// detectColor reports whether ANSI escapes should be emitted.
// DAPR_INTEGRATION_LOGS_COLOR forces the decision either way, otherwise colour
// is used on GitHub Actions (which renders ANSI) and on a local terminal.
func detectColor() bool {
	if v, ok := os.LookupEnv("DAPR_INTEGRATION_LOGS_COLOR"); ok {
		return kitstrings.IsTruthy(v)
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return true
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// colorize wraps s in the given ANSI codes. Callers must pad s to its final
// width first, since the escapes are invisible but still count as bytes.
func colorize(s string, codes ...string) string {
	if !useColor || len(codes) == 0 || len(s) == 0 {
		return s
	}
	return strings.Join(codes, "") + s + ansiReset
}

// rule returns a horizontal rule of n cells, falling back to ASCII where the
// terminal cannot be trusted to render box drawing characters.
func rule(n int) string {
	if useUnicode {
		return strings.Repeat("─", n)
	}
	return strings.Repeat("-", n)
}
