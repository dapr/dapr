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
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dapr/kit/logger"
	kitstrings "github.com/dapr/kit/strings"
)

// Failure is a test which produced a log report, and the file it was written
// to.
type Failure struct {
	Test string
	Path string
}

var (
	failuresLock sync.Mutex
	failures     []Failure
)

// LogDir is the directory reports are written to. It is deliberately outside
// t.TempDir() so that the files survive the test run that produced them.
func LogDir() string {
	if dir := os.Getenv("DAPR_INTEGRATION_LOGS_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "dapr_integration_logs")
}

// ResetLogDir removes the log files of the previous run, so that
// `less <dir>/*.log` shows this run and not the last one. Concurrent runs on one
// machine should set DAPR_INTEGRATION_LOGS_DIR to keep out of each other's way.
//
// Only files this package wrote are removed. DAPR_INTEGRATION_LOGS_DIR may point
// anywhere, so emptying the directory wholesale is not something to do.
func ResetLogDir() error {
	dir := LogDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}

	return nil
}

// inProcessLog holds the output of dapr packages running inside the test
// binary, as opposed to inside one of the processes a test starts.
const inProcessLog = "in-process.log"

// RedirectInProcessLogs sends the output of code running inside the test binary
// itself, rather than inside a process a test started, to a file. It reports
// where it wrote.
//
// This output belongs to no particular test, so the framework cannot attribute
// it to one, and it lands on the terminal in the middle of a run. Two sources
// produce it:
//
//   - dapr's own logger, which packages such as pkg/security register at init
//     and which writes to stderr;
//   - the standard library logger, used directly by a handful of test apps and,
//     more importantly, by net/http for things like "TLS handshake error" when
//     a server has no ErrorLog of its own.
//
// Only dapr loggers already registered are redirected. Packages register theirs
// at init, so calling this before the run starts covers them; a logger created
// later still writes to stderr. The standard library redirect has no such
// caveat, since there is a single logger behind log.Printf.
//
// Set DAPR_INTEGRATION_INPROCESS_LOGS to leave everything on stderr.
func RedirectInProcessLogs() (string, error) {
	if kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_INPROCESS_LOGS")) {
		return "", nil
	}

	if err := os.MkdirAll(LogDir(), 0o750); err != nil {
		return "", err
	}

	path := filepath.Join(LogDir(), inProcessLog)

	// Deliberately left open: it is wanted for the lifetime of the process, and
	// the process is a test binary that is about to exit anyway.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	log.SetOutput(f)

	opts := logger.DefaultOptions()
	opts.OutputFile = path
	if err := logger.ApplyOptionsToLoggers(&opts); err != nil {
		return "", err
	}

	return path, nil
}

// inline reports whether reports go to the test output rather than to a file.
//
// A full suite run writes tens of thousands of log lines, which drown the
// failures they explain, so locally they go to files. GitHub Actions collapses
// each test's output into a log group, so there inlining costs nothing and
// saves downloading an artifact to read a failure.
func inline() bool {
	if v, ok := os.LookupEnv("DAPR_INTEGRATION_LOGS_INLINE"); ok {
		return kitstrings.IsTruthy(v)
	}
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

// Failures returns every test which wrote a report, in the order they finished.
func Failures() []Failure {
	failuresLock.Lock()
	defer failuresLock.Unlock()

	out := make([]Failure, len(failures))
	copy(out, failures)

	return out
}

func recordFailure(test, path string) {
	failuresLock.Lock()
	defer failuresLock.Unlock()
	failures = append(failures, Failure{Test: test, Path: path})
}

// writeReport writes the report for a test to its own file, returning the path.
func writeReport(test, report string) (string, error) {
	dir := LogDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}

	path := filepath.Join(dir, logFileName(test))
	if err := os.WriteFile(path, []byte(stripANSI(report)), 0o600); err != nil {
		return "", err
	}

	return path, nil
}

// logFileName turns a test name into a flat file name, so that the whole run
// lands in one directory that is easy to grep and easy to delete.
func logFileName(test string) string {
	var sb strings.Builder
	for _, r := range test {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-', r == '_', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteRune('.')
		}
	}

	return sb.String() + ".log"
}

// stripANSI removes colour escapes, which help in a terminal but only get in
// the way of grep and of an editor opening the file.
func stripANSI(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			sb.WriteByte(s[i])
			continue
		}
		// Skip up to and including the escape's terminating letter.
		for i++; i < len(s); i++ {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				break
			}
		}
	}

	return sb.String()
}
