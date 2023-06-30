# Writing an integration test

## Introduction

Integration tests are run against a live running daprd binary locally. Each test
scenario is run against a new instance of daprd, where each scenario modifies
the daprd configuration to suit the test. Tests are expected to complete within
seconds, ideally less than 5, and should not take longer than 30. Daprd is
always built from source within the test.


## Invoking the test

```bash
go test -v -race -count -tags="integration" ./tests/integration` -run="Test_Integration/daprd/pubsub/http/fuzzpubsubNoRaw"
```

Rather than building from source, you can also set a custom daprd binary path
with the environment variable `DAPR_INTEGRATION_DAPRD_PATH`.

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
