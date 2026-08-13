# Writing an integration test

## Introduction

Integration tests are run against a live running daprd binary locally. Each test
scenario is run against a new instance of daprd, where each scenario modifies
the daprd configuration to suit the test. Tests are expected to complete within
seconds, ideally less than 5, and should not take longer than 30. Binaries are
always built from source within the test.

You can find out more about the background and design decisions of the integration tests through a talk by joshvanl [here](https://www.youtube.com/watch?v=CcaV5_rQBzY).


## Invoking the test

```bash
go test -race -tags integration ./tests/integration
```

**Do not pass `-v`.** It makes Go print a `=== RUN` and a `--- PASS` line for
every test and subtest, which is over eight thousand lines across the suite and
buries the failures.

Without it, the run draws a progress line on your terminal instead:

```
 ⠹ 512/1347  ok 510  fail 2  1m04s  actors/reminders/period
```

It updates in place as tests finish, so the run is never silent, and failures
are printed above it the moment they happen:

```
  FAIL  actors/deactivation/move
```

The line is drawn on the terminal, not on stdout, so `go test > out.txt` still
gets clean output while you watch progress on screen. It turns itself off when
there is no terminal, on CI, and under `-v`. `DAPR_INTEGRATION_PROGRESS=false`
disables it.

When everything passes, all that is left behind is:

```
ok  	github.com/dapr/dapr/tests/integration	204.534s
```

Run a subset with the `-focus` flag, which takes a [Go regular expression](https://github.com/google/re2/wiki/Syntax).

```bash
# Run all sentry related tests.
go test -race -tags integration ./tests/integration -focus sentry

# Run all sentry related tests whilst skipping the sentry jwks validator test.
go test -race -tags integration ./tests/integration -test.skip Test_Integration/sentry/validator/jwks -focus sentry
```

To run integration tests several times for debugging purposes use this configuration and change the focus and count as needed:
```bash
go test -race -tags integration ./tests/integration -focus scheduler/authz --count=100 -failfast
```

For a live view of a long run, use the dots format, which prints one character
per test as it completes:

```bash
make test-integration-dots
make test-integration-dots FOCUS=sentry
```

Rather than building from source, you can also set a custom daprd, sentry, or placement binary path with the environment variables:
- `DAPR_INTEGRATION_DAPRD_PATH`
- `DAPR_INTEGRATION_PLACEMENT_PATH`
- `DAPR_INTEGRATION_SENTRY_PATH`

## Reading the output

A passing test prints nothing beyond its own result line. A failing test prints
its assertion and one more line, pointing at the log file for that test:

```
    daprd.go:60: Error: Not equal: expected 1234, actual 1027
    logs: /tmp/dapr_integration_logs/ports.daprd.log
```

Process logs are never dumped into the terminal, because a full suite run
produces tens of thousands of lines of them. At the end of the run every failure
is listed again with its log file:

```
1 of 1347 test cases failed:
  ports/daprd  /tmp/dapr_integration_logs/ports.daprd.log
Read all of them with: less /tmp/dapr_integration_logs/*.log
```

The directory is emptied at the start of each run. Set
`DAPR_INTEGRATION_LOGS_DIR` to change it, which is also how to run two suites at
once without them overwriting each other.

Inside a log file is what the harness did, followed by one block per process,
each line prefixed with how long after the test started it was written:

```
──── logs: scheduler/authz/Authz (FAIL after 4.21s) ─────────────────────────
── framework ──
    0.000s  starting 3 processes
    0.004s  exec scheduler: /tmp/dapr_integration_tests/scheduler --port=41231
    4.208s  scheduler exited (code 0, want 0)
── daprd app_id=myapp ──
    0.402s  INFO   dapr.runtime  dapr initialized. Status: Running
    1.190s  ERROR  dapr.runtime  failed to connect to scheduler
─────────────────────────────────────────────────────────────────────────────
```

Where a test runs several instances of the same process, the second and later
instances are reported as `daprd-1`, `daprd-2` and so on.

Log files are written whether or not `go test` is verbose, so
`DAPR_INTEGRATION_LOGS=true` gives you a directory of logs to browse after a run
without putting a single extra line on your terminal:

```bash
DAPR_INTEGRATION_LOGS=true go test -race -tags integration ./tests/integration -focus actors/reminders
less /tmp/dapr_integration_logs/*.log
```

Some dapr packages, such as `pkg/security`, run inside the test binary rather
than inside a process a test started. They register a logger at init and write
straight to stderr, which belongs to no particular test and which `go test` will
happily print in the middle of a run:

```
INFO[0002] Starting workload identity expiry watcher; cert expires on: ...  scope=dapr.runtime.security ver=unknown
```

Those go to `in-process.log` in the same directory instead. Set
`DAPR_INTEGRATION_INPROCESS_LOGS=true` to leave them on stderr.

The report is controlled by these environment variables:

- `DAPR_INTEGRATION_LOGS=true` writes a report even when the test passes.
- `DAPR_INTEGRATION_LOGS_INLINE=true` puts the report in the test output instead
  of a file. This is already the default on GitHub Actions, which collapses each
  test's output into a log group, so a CI failure can be read without
  downloading anything. Locally it is only sensible for a small focus.
- `DAPR_INTEGRATION_LOGS=stream` writes lines to stderr as they arrive instead of
  at the end. Useful when focused on a single test, since the output of tests
  running in parallel interleaves.
- `DAPR_INTEGRATION_LOGS_DIR` sets the directory log files are written to.
- `DAPR_INTEGRATION_LOGS_RAW=true` disables log line parsing, so each line is
  shown exactly as the process wrote it.
- `DAPR_INTEGRATION_LOGS_COLOR=false` disables colour, which is otherwise used on
  a terminal and on GitHub Actions. `NO_COLOR` is also honoured.

You can override the directory that is used to read the CRD definitions that are served by the Kubernetes process with the environment variable `DAPR_INTEGRATION_CRD_DIRECTORY`.

Setting `DAPR_INTEGRATION_WORKFLOW_CLUSTERED=true` enables the
`WorkflowsClusteredDeployment` preview feature on every daprd built by the
workflow test framework, running the workflow suite in clustered deployment
mode. Tests can override this per workflow with
`workflow.WithClusteredDeployment(bool)`, and branch mode-specific assertions
on `workflow.ClusteredDeployment()`. CI runs the workflow suite in this mode as
a leg of the `integration-tests-workflow-modes` matrix job.

## Adding a new test

To add a new test scenario, either create a new subject directory in
`tests/integration/suite` and create a new file there, or use an existing
subject directory if appropriate. Each test scenario is represented by a
`struct` which implements the following interface. The `struct` name is used as
the test name.

```go
type Case interface {
	Setup(*testing.T) []framework.RunDaprdOption
	Run(*testing.T, *framework.Command)
}
```

To add the test to the suite, add the following `init` function

```go
func init() {
	suite.Register(new(MyNewTestScenario))
}
```

Finally, include your integration test directory with a blank identifier to
`tests/integration/integration.go` so that the init function is invoked.

```go
	_ "github.com/dapr/dapr/tests/integration/suite/my-new-test-scenario"
```

You may need to extend the framework options to suit your test scenario. These
are defined in `tests/integration/framework`.

Take a look at `tests/integration/suite/ports/ports.go` as a "hello world"
example to base your test on.
