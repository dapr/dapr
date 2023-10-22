# Check Lint Version

This package is designed to check the local golangci-lint version against that of the current github workflow.

## Usage

In the repo root, you can use the `make lint` command which makes use of this to verify the golangci-lint version and
run the linter.

## Workflow

The `test-tooling` workflow is responsible for testing this package.