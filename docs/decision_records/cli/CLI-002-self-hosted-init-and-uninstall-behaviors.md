# CLI-002: Self-Hosted mode Init and Uninstall behaviours

## Status
Accepted

## Context
Changes in behavior of `init` and `uninstall` on Self Hosted mode for. Discussed in this [issue](https://github.com/dapr/cli/issues/411).

## Decisions

* Calling `dapr init` will
  * Install `daprd` binary in `$HOME/.dapr/bin` for Linux/MacOS and `%USERPROFILE%\.dapr\bin` for Windows.
  * Set up the `dapr_placement`, `dapr_redis` and `dapr_zipkin` containers.
  * Create the default `components` folder in `$HOME/.dapr/bin` for Linux/MacOS or `%USERPROFILE\.dapr\bin` for Windows.
  * Create the default components configurations for `pubsub.yaml`, `statestore.yaml` and `zipkin.yaml` in the default `components` folder.
  * Create a default configuration file in `$HOME/.dapr/config.yaml` for Linx/MacOS and `%USERPROFILE%\.dapr\config.yaml` for Windows for enabling tracing by default.
* Calling `dapr init --slim` will
  * Install the binaries `daprd` and `placement` in `$HOME/.dapr/bin` for Linux/MacOS and `%USERPROFILE%\.dapr\bin` for Windows.
  * Create an empty default `components` folder in `$HOME/.dapr/bin` for Linux/MacOS or `%USERPROFILE\.dapr\bin` for Windows.
* Calling `dapr uninstall` will
  * Remove the folder containing binaries `bin` the default path `$HOME/.dapr` for Linux/MacOS and `%USERPROFILE%\.dapr` for Windows.
  * Remove the docker dapr_placement if Docker is installed.
* Calling `dapr uninstall --all`
  * Remove the folder containing binaries `bin` the default path `$HOME/.dapr` for Linux/MacOS and `%USERPROFILE%\.dapr` for Windows.
  * Remove the docker containers dapr_placement, dapr_redis and dapr_zipkin if Docker is installed.
  * Remove the default folder `$HOME/.dapr` in Linux/MacOS and `%USERPROFILE%\.dapr` in Windows.
* CLI on the init command will fail if a prior installtion exists in the default path `$HOME/.dapr` for Linux/MacOS and `%USERPROFILE%\.dapr` for Windows.
* **There will no longer be an option for `--install-path` during init or during uninstall.**
* The `dapr` CLI by default will expect the `daprd` in `$HOME/.dapr/bin` for Linux/MacOS and `%USERPROFILE%\.dapr\bin` for Windows. The command `dapr run` will not expect the `daprd` binary to be in the `PATH` variable, it will launch the binary from the default path.
  
## Consequences

All other binaries except `dapr` cli and configurations that dapr needs (on running `dapr init`)will be placed in the path  `$HOME/.dapr/bin` for Linux/MacOS and `%USERPROFILE%\.dapr\bin` for Windows.
