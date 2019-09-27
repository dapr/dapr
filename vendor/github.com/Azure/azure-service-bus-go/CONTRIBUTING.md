# Contributing

## Getting Started

First of all, welcome. Thank you for your interest in furthering Go support for Azure Service Bus.

### Workstation Requirements

The following programs should be installed on your machine before you begin developing.

> Note: Adhering to the linters below is enforced in CI. It is not required to have the tools locally, but contributors 
are expected to fix those issues found in CI.

| Tool | Necessary | Description |
| :---------------------------------------------------: | :-----: | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [go](https://godoc.org)                               | Required |This is a Go project, and as such you should have Go installed on your machine. We use modules for package management, so we recommend at least using go1.11. However, you may also use go1.9.7+ or go1.10.3+. |
| [git](https://git-scm.com)                            | Required |azure-service-bus-go uses Git as its source control management solution.                                                                                                            |
| [az](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli?view=azure-cli-latest) | Optional | The Azure CLI is used for its ability to authenticate against Azure. Tests themselves only need a connection string and other metadata about the Service Bus namespace, but tooling to automatically setup the necessary infrastructure to run the tests may depend on the Azure CLI. |
| [terraform](https://terraform.io)                     | Optional | `terraform` is used to help provision the Azure infrastructure needed to run our tests both in CI and on your local machine. If you have already provisioned a Service Bus Namespace, and created the necessary Service Bus Queues, you do not need terraform. |
| [golint](https://godoc.org/golang.org/x/lint/golint)  | Optional |`golint` is a linter that finds basic stylistic mistakes in Go programs.                                                                                                            |
| [gocyclo](https://github.com/fzipp/gocyclo)           | Optional |`gocyclo`  checks for programmatic complexity, to ensure code readability.                                                                                                          |
| [megacheck](https://honnef.co/go/tools/cmd/megacheck) | Optional | `megacheck` is a linter that checks for a broader set of errors than `go vet` or `golint`.                                                                                         |

#### Editors

Feel free to use your editor of choice for modifying this project! We use both both [VS Code](https://code.visualstudio.com)
and [Goland](https://www.jetbrains.com/go/). Whichever editor you choose, please do not commit any project configuration
files such as the contents of the `.vscode/` or `.idea/` directories.

### License Agreement

In order for us to accept your contribution, you must have signed the the [Microsoft Open Source Contribution License
Agreement](https://cla.opensource.microsoft.com/Azure/azure-service-bus-go). It only takes a minute, and is attached to
your GitHub account. Sign once and commit to any Microsoft Open Source Project.

## Running Tests

1. Ensure that you have an App Registration (Service Principal) with a Key setup with access to your subscription.
	- [Azure AAD Application Documenation](https://docs.microsoft.com/en-us/azure/azure-resource-manager/resource-group-create-service-principal-portal)
	- [HashiCorp Azure Service Principal Documentation](https://www.terraform.io/docs/providers/azurerm/authenticating_via_service_principal.html) 
1. Set the following environment variables:

	| Name                           | Contents                                                                                                      |
	| :----------------------------: | :------------------------------------------------------------------------------------------------------------ |
	| SERVICEBUS_CONNECTION_STRING   | The escaped connection string associated with the Service Bus namespace that should be targeted.              |
	| AZURE_CLIENT_ID                | The Application ID of the App Registration (i.e. Service Principal) to be used to create test infrastructure. |
	| AZURE_CLIENT_SECRET            | The Key associated with the App Registration to be used to create test infrastructure.                        |
	| AZURE_SUBSCRIPTION_ID          | The Azure Subscription to be used to run your tests.                                                          |
	| AZURE_TENANT_ID                | The UUID used to identify the tenant your Azure Subscription belongs to.                                      |
	| TEST_SERVICEBUS_RESOURCE_GROUP | The Azure Resource Group that holds the infrastructure needed to run the tests.                               |
	| TEST_SERVICEBUS_LOCATION       | The Azure Region containing your resource group. (e.g. "eastus", "westus2", etc.)                             |
1. Authenticate using the CLI byt running the command `az login`. 
	> Note: Alternatively, set environment variables `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET`, `ARM_TENANT_ID`, and `ARM_SUBSCRIPTION_ID` equal to their AZURE counterparts.
1. Run `terraform apply` to make sure that all of the infrastructure needed to run the tests is available. 
	> Note: you can save values that it asks you for by defining them in a [file named `terraform.tfvars`](https://www.terraform.io/intro/getting-started/variables.html).
1. Run the tests by executing `go test` from the repository's root directory.

 

#### Linux + MacOS

Running the command `make test` will automatically run all linting rules, terraform, and `go test` for you.


## Filing Issues

If you feel that you've found a way to improve Azure Service Bus itself, or with this library, feel free to open an 
issue here in GitHub. We'll see that it gets into the hands of the appropriate people, whomever that is. 

### Bugs

When filing an issue to bring awareness to a bug, please provide the following information:
- The OS and Go version you are using. i.e. the output of running `go version`.
- The version of Azure-Service-Bus-Go you are using.

It also significantly speeds things up if you can provide the minimum amount of code it takes to reproduce the bug in
the form of a [GitHub Gist](https://gist.github.com) or [Go Playground](https://play.golang.org) snippet.

### Feature Requests

For expanded capabilities, please describe what you'd like to see and whom you believe would benefit.

## Reference

- [Clemens Vaster explains AMQP 1.0 - youtube.com](https://www.youtube.com/playlist?list=PLmE4bZU0qx-wAP02i0I7PJWvDWoCytEjD)
- [Service Bus AMQP Protocol Guide - docs.microsoft.com](https://docs.microsoft.com/en-us/azure/service-bus-messaging/service-bus-amqp-protocol-guide)
- [.NET Service Bus Library - github.com](https://github.com/Azure/azure-service-bus-dotnet)
