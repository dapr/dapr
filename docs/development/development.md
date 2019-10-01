
# Development Guide

This document helps you get started developing Actions. If you find any problem while following this guide, please create a Pull Request to update this document.

## Prerequisites

### Linux and MacOS

1. The Go language environment [(instructions)](https://golang.org/doc/install#tarball).
   * Make sure that your GOPATH and PATH are configured correctly
   ```bash
   export GOPATH=~/go
   export PATH=$PATH:$GOPATH/bin
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Debugging

### Windows

1. The Go language environment [(instructions)](https://golang.org/doc/install#windows).
   Make sure that your GOPATH and PATH are configured correctly - You may set environment variables through the "Environment Variables" button on the "Advanced" tab of the "System" control panel. Some versions of Windows provide this control panel through the "Advanced System Settings" option inside the "System" control panel.
   ```
   GOPATH=c:\go
   PATH=%GOPATH%\bin;...
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Debugging
3. [Git for Windows](https://gitforwindows.org)
   * Install [Git with chocolatey](https://chocolatey.org/packages/git) and ensure that Git bin directory is in PATH environment variable
    ```bash
    choco install git -y --package-parameters="/GitAndUnixToolsOnPath /WindowsTerminal /NoShellIntegration"
    ```
4. [MinGW](http://www.mingw.org/)
  Install [MinGW with chocolatey](https://chocolatey.org/packages/mingw) and ensure that MinGW bin directory is in PATH environment variable

    ```bash
    choco install mingw
    ```

## Clone the repo

```bash
cd $GOPATH/src
mkdir -p github.com/actionscore/actions
git clone https://github.com/actionscore/actions.git github.com/actionscore/actions
```

## Build the Actions binary

You can build actions binaries via `make` tool and find the binaries in `./dist/{os}_{arch}/release/`.

> Note : for windows environment with MinGW, use `mingw32-make.exe` instead of `make`.

* Build for your current local environment

```bash
cd $GOPATH/src/github.com/actionscore/actions/
make build
```

* Cross compile for multi platforms

```bash
make build GOOS=linux GOARCH=amd64
```

## Run unit-test

```bash
make test
```

## Debug actions

We highly recommend to use [VSCode with Go plugin](https://marketplace.visualstudio.com/items?itemName=ms-vscode.Go) for your productivity. If you want to use the different editors, you can find the [list of editor plugins](https://github.com/go-delve/delve/blob/master/Documentation/EditorIntegration.md) for Delve.

This section introduces how to start debugging with Delve CLI. Please see [Delve documentation](https://github.com/go-delve/delve/tree/master/Documentation) for the detail usage.

### Start with debugger

```bash
$ cd $GOPATH/src/github.com/actionscore/actions/actionsrt
$ dlv debug .
Type 'help' for list of commands.
(dlv) break main.main
(dlv) continue
```

### Attach Debugger to running binary

This is useful to debug actions when the process is running.

1. Build actions binaries for debugging
   With `DEBUG=1` option, actions binaries will be generated without code optimization in `./dist/{os}_{arch}/debug/`

```bash
$ make DEBUG=1 build
```

2. Create component yaml file under `./dist/{os}_{arch}/debug/components` e.g. statstore component yaml
3. Run actions runtime
4. Find the process id and attach the debugger

```bash
$ dlv attach [pid]
```

### Debug unit-tests

```bash
# Specify the package that you want to test
# e.g. debuggin ./pkg/actors
$ dlv test ./pkg/actors
```
