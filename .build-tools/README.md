# Dapr build-tools CLI

This folder contains a CLI that implements a number of build tools used by Dapr.

The CLI is written in Go and based on the [cobra](https://github.com/spf13/cobra) framework. In order to use it, Go (1.18+) must be installed on your system.

## Running the CLI

You have two ways to run the CLI:

1. Within the `build-tools` directly, you can run the CLI with `go run .` directly. For example `go run . help` shows the help page.
2. You can build a pre-compiled binary by running `make compile-build-tools` in the root folder of this repository. This will build an executable called `build-tools` (or `build-tools.exe` on Windows) in the `build-tools` folder. You can then run the command directly, for example `./build-tools help`

## Available commands

The list of available commands in this CLI is dynamic and is subject to change at any time. Each command, including the "root" one (no sub-command), are self-documented in the CLI, and you can read the help page by adding `--help`.

For example, `./build-tools --help` shows the full list of commands the CLI offers. `./build-tools e2e --help` shows the help page for the `e2e` sub-command.
