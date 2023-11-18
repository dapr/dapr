# Writing an integration test

## Introduction

Integration tests are run against a live running daprd binary locally. Each test
scenario is run against a new instance of daprd, where each scenario modifies
the daprd configuration to suit the test. Tests are expected to complete within
seconds, ideally less than 5, and should not take longer than 30. Binaries are
always built from source within the test.


## Invoking the test

```bash
go test -v -race -tags integration ./tests/integration
```

You can also run a subset of tests by specifying the `-focus` flag, which takes a [Go regular expression](https://github.com/google/re2/wiki/Syntax).

```bash
# Run all sentry related tests.
go test -v -race -tags integration ./tests/integration -focus sentry

# Run all sentry related tests whilst skipping the sentry jwks validator test.
go test -v -race -tags integration ./tests/integration -test.skip Test_Integration/sentry/validator/jwks -focus sentry
```

Rather than building from source, you can also set a custom daprd, sentry, or placement binary path with the environment variables:
- `DAPR_INTEGRATION_DAPRD_PATH`
- `DAPR_INTEGRATION_PLACEMENT_PATH`
- `DAPR_INTEGRATION_SENTRY_PATH`

By default, binary logs will not be printed unless the test fails. You can force
logs to always be printed with the environment variable
`DAPR_INTEGRATION_LOGS=true`.

You can override the directory that is used to read the CRD definitions that are served by the Kubernetes process with the environment variable `DAPR_INTEGRATION_CRD_DIRECTORY`.

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
