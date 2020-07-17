# CLI-002: Self-Hosted mode Init and Uninstall behaviours

## Status
Accepted

## Context
Changes in behavior of `init` and `uninstall` on Self Hosted mode for. Discussed in this [issue](https://github.com/dapr/cli/issues/380).

## Decisions

* Calling `dapr init` will
  * Install `daprd` binary in `/usr/local/bin` for Linux/MacOS and `C:\dapr` for Windows.
  * Set up the `dapr_placement`, `dapr_redis` and `dapr_zipkin` containers.
  * Create the default components folder in `$HOME/.dapr` for Linux/MacOS or `%USERPROFILE\.dapr` for Windows.
  * Create the default components configurations for `pubsub.yaml`, `statestore.yaml` and `zipkin.yaml` in the default components folder.
  * Create a default configuration file in `$HOME/.dapr/config.yaml` for Linx/MacOS and `%USERPROFILE%\.dapr\config.yaml` for Windows for enabling tracing by default.
* Calling `dapr init --slim` will
  * Install the binaries `daprd` and `placement` in `/usr/local/bin` for Linux/MacOS and `C:\dapr` for Windows.
  * Create the empty default components folder in `$HOME/.dapr` for Linux/MacOS or `%USERPROFILE\.dapr` for Windows.
* CLI on the init command will fail if a prior installtion exists in the default paths `/usr/local/bin` for Linux/MacOS or `C:\dapr` for Windows or the provided install path using the `--install-path` flag.
* Calling `dapr uninstall` will
  * Remove the binary daprd (and placement for slim init) from the default path `/usr/local/bin` for Linux/MacOS and `C:\dapr` for Windows. The path can be overridden with a `--install-path` flag.
  * Remove the docker dapr_placement if Docker is installed.
* Calling `dapr uninstall --all`
  * * Remove the binary daprd (and placement for slim init) from the default path `/usr/local/bin` for Linux/MacOS and `C:\dapr` for Windows. The path can be overridden with a `--install-path` flag.
  * Remove the docker containers dapr_placement, dapr_redis and dapr_zipkin if Docker is installed.
  * Remove the default folder `$HOME/.dapr` in Linux/MacOS and `%USERPROFILE%\.dapr` in Windows.
  
## Consequences

This will require Linux users, running in standalone mode  to use `sudo dapr init` for initialization(existing behavior) and also run `sudo dapr uninstall` for uninstall calls.
