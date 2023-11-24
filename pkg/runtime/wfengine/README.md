# Dapr Workflow Engine

The Dapr Workflow engine enables developers to author workflows using code and execute them using the Dapr sidecar. You can learn more about this project here: [[Proposal] Workflow building block and engine (#4576)](https://github.com/dapr/dapr/issues/4576).

This README is designed to be used by maintainers to help with getting started. This README will be updated with more information as the project progresses.

## Building Daprd

The workflow engine is entirely encapsulated within the [dapr sidecar (a.k.a. daprd)](https://docs.dapr.io/concepts/dapr-services/sidecar/). All dependencies are compiled directly into the binary.

Internally, this engine depends on the [Durable Task Framework for Go](https://github.com/microsoft/durabletask-go), an MIT-licensed open-source project for authoring workflows (or "orchestrations") as code. Use the following command to get the latest build of this dependency:

```bash
go get github.com/microsoft/durabletask-go
```

Be mindful, that the above command will also pull in dependencies for sqlite, which we don't want or require. Those can be manually removed from go.mod and go.sum.

The following bash command can be used to build a version of Daprd that supports the workflow engine.

```bash
DEBUG=1 make build
```
* `DEBUG=1` is required to attach debuggers. This should never be set for production or performance testing workloads.

After building, the following bash command can be run from the project root to test the code:

```bash
./dist/linux_amd64/debug/daprd --app-id wfapp --dapr-grpc-port 4001 --placement-host-address :6050 --components-path ~/.dapr/components/ --config ~/.dapr/config.yaml
```
* The gRPC port is set to `4001` since that's what the Durable Task test clients default to.
* This assumes a placement service running locally on port `6050` (the default).
* This assumes a basic actor-compatible state store is configured in `~/.dapr/components`.
* You should see logs with `scope=dapr.runtime.wfengine` if the workflow engine is enabled in your build.

Here's an example of the log output you'll see from Dapr when the workflow engine is enabled:

```
INFO[0000] configuring workflow engine gRPC endpoint          app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
INFO[0000] configuring workflow engine with actors backend    app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
INFO[0000] Registering component for dapr workflow engine...  app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
INFO[0000] Initializing Dapr workflow engine                  app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
```

Note that the workflow engine doesn't fully start up until an application opens a work-item stream on it, after which you'll see the following logs:

```
INFO[0146] work item stream established by user-agent: XYZ   app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
INFO[0146] worker started with backend dapr.actors/v1-alpha  app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
INFO[0146] workflow engine started                           app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
```

If you want to see the full set of logs, run daprd with verbose logging enabled (`--log-level debug`). You'll see a few additional logs in this case, indicating that the workflow engine is waiting for new work items:

```
DEBU[0000] orchestration-processor: waiting for new work items...  app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
DEBU[0000] activity-processor: waiting for new work items...       app_id=wfapp instance=XYZ scope=dapr.runtime.wfengine type=log ver=edge
```

## Running tests

### Unit tests

Unit tests can be run using the following `go` command from the repo root. Depending on the speed of your development machine, these tests should complete in less than 30 seconds.

```bash
go test ./pkg/runtime/wfengine/... -tags=unit
```

If you're using VS Code, you can also run tests directly from the IDE.

### Manual testing

There are no end-to-end tests that directly target the Dapr Workflows engine yet. However, this engine is fully compatible with .NET and Java Durable Task SDKs.

| Language/Stack | Package | Project Home | Samples |
| - | - | - | - |
| .NET | [![NuGet](https://img.shields.io/nuget/v/Microsoft.DurableTask.Client.svg?style=flat)](https://www.nuget.org/packages/Microsoft.DurableTask.Client/) | [GitHub](https://github.com/microsoft/durabletask-dotnet) | [Samples](https://github.com/microsoft/durabletask-dotnet/tree/main/samples) |
| Java | [![Maven Central](https://img.shields.io/maven-central/v/com.microsoft/durabletask-client?label=durabletask-client)](https://search.maven.org/artifact/com.microsoft/durabletask-client) | [GitHub](https://github.com/microsoft/durabletask-java) | [Samples](https://github.com/microsoft/durabletask-java/tree/main/samples/src/main/java/io/durabletask/samples) |

You can also run the samples above and have them execute end-to-end with Dapr running locally on the same machine. The samples connect to gRPC over port `4001` by default, which will work without changes as long as Dapr is configured with `4001` as its gRPC port (like in the example above).

### Durable Task integration testing

For quick integration testing, you can run the following docker command which runs a suite of integration tests used by the official Durable Task .NET SDK:

```bash
docker run -e GRPC_HOST="host.docker.internal" cgillum/durabletask-dotnet-tester:0.5.0-beta
```

Note that the test assumes the daprd process can be reached over `localhost` with port `4001` as the gRPC port on the host machine. These values can be overridden with the following environment variables:

* `GRPC_HOST`: Use this to change from the default `127.0.0.1` to some other value, for example `host.docker.internal`.
* `GRPC_PORT`: Set this environment variable to change the default port from `4001` to something else.

If successful, you should see output that looks like the following:

```
Test run for /root/out/bin/Debug/Microsoft.DurableTask.Tests/net6.0/Microsoft.DurableTask.Tests.dll (.NETCoreApp,Version=v6.0)
Microsoft (R) Test Execution Command Line Tool Version 17.3.1 (x64)
Copyright (c) Microsoft Corporation.  All rights reserved.

Starting test execution, please wait...
A total of 1 test files matched the specified pattern.
[xUnit.net 00:00:00.00] xUnit.net VSTest Adapter v2.4.3+1b45f5407b (64-bit .NET 6.0.10)
[xUnit.net 00:00:00.82]   Discovering: Microsoft.DurableTask.Tests
[xUnit.net 00:00:00.90]   Discovered:  Microsoft.DurableTask.Tests
[xUnit.net 00:00:00.90]   Starting:    Microsoft.DurableTask.Tests
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.ExternalEvents(eventCount: 100) [6 s]
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.ExternalEvents(eventCount: 1) [309 ms]
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.LongTimer [8 s]
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.SubOrchestration [1 s]
  ...
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.ActivityFanOut [914 ms]
[xUnit.net 00:01:01.04]   Finished:    Microsoft.DurableTask.Tests
  Passed Microsoft.DurableTask.Tests.OrchestrationPatterns.SingleActivity_Async [365 ms]

Test Run Successful.
Total tests: 33
     Passed: 33
 Total time: 1.0290 Minutes
```

## How the workflow engine works

The Dapr Workflow engine introduced a new concept of *internal actors*. These are actors that are registered and implemented directly in Daprd with no host application dependency. Just like regular actors, they have turn-based concurrency, support reminders, and are scaled out using the placement service. Internal actors also leverage the configured state store for actors. The workflow engine uses these actors as the core runtime primitives for workflows.

Each workflow instance corresponds to a single `dapr.internal.wfengine.workflow` actor instance. The ID of the workflow instance is the same as the internal actor ID. The internal actor is responsible for triggering workflow execution and for storing workflow state. The actual workflow logic lives outside the Dapr sidecar in a host application. The host application uses a new gRPC endpoint on the daprd gRPC API server to send and receive workflow-specific commands to/from the actor-based workflow engine. The workflow app doesn't need to take on any actor dependencies, nor is it aware that actors are involved in the execution of the workflows. Actors are purely an implementation detail.

### State storage

Each workflow actor saves its state using the following keys:

* `metadata`: Contains meta information about the workflow as a JSON blob. Includes information such as the length of the inbox, the length of the history, and a 64-bit integer representing the workflow generation (for cases where the instance ID gets reused). The length information is used to determine which keys need to be read or written to when loading or saving workflow state updates.
* `inbox-NNNNNN`: Multiple keys containing an ordered list of workflow inbox events. Each key holds the data for a single event. The inbox is effectively a FIFO queue of events that the workflow needs to process, with items removed from the earlier indices and added to the end indices.
* `history-NNNNNN`: Multiple keys containing an ordered list of history events. Each key holds the data for a single event. History events are only added and never removed, except in the case of "continue as new", where all history events are purged.
* `customStatus`: Contains a user-defined workflow status value.

The `inbox-NNNNNN` and `history-NNNNNN` key schemes are used to enable arbitrarily large amounts of data. These schemes are also designed for efficient updates. An alternate design would be to store the workflow history as a blob in a single key. However, this would limit the maximum size of the history and would make updates more expensive, since the full history would need to be serialized instead of just inserting incremental additions (the history is an append-only log of events).

The tradeoff with this key scheme design is that loading workflow state becomes more expensive since it's spread out across multiple keys. This is mitigated by the fact that actor state can be cached in memory, removing the need for any reads while the actors are active. However, it could be a problem if workflow histories get large and if actors get moved around or activated frequently.

Below is an example of what keys would be used to store the state of a simple workflow execution with ID '797f67f0c10846f592d0ac82dea1f248', as shown using `redis-cli`.

```
127.0.0.1:6379> keys *797f67f0c10846f592d0ac82dea1f248*
1) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000002"
2) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||customStatus"
3) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||metadata"
4) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000003"
5) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000005"
6) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000001"
7) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000000"
8) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||history-000004"
9) "myapp||dapr.internal.wfengine.workflow||797f67f0c10846f592d0ac82dea1f248||inbox-000000"
```

**IMPORTANT**: At the time of writing, there is no automatic purging of state for completed workflows. This means that the configured state store will continue to acquire new state indefinitely as more workflows are executed. Until automatic cleanup is implemented, old state will need to be purged manually from the configured state store.

### Resiliency

Workflows are resilient to infrastructure failures. This is achieved by using reminders to drive all execution. If a process faults mid-execution, the reminder that initiated that execution will get scheduled again by Dapr to resume the execution from it's previous checkpoint, which is stored in the state store. 

At all times, there is at least one reminder active for each workflow. However, there is typically a different reminder created for each *step* in the workflow. Here's an example of all the reminders that may get created as part of running a full end-to-end workflow.

| Reminder name | Description | Payload? |
| - | - | - |
| `start`        | Triggers the initial execution step of a workflow after it's created. | No |
| `new-event`    | Triggers subsequent processing of events by a workflow. | No |
| `timer`        | A special event reminder for a *durable timer* that is scheduled to run sometime in the future. | Yes, the durable task history event associated with the durable timer. |
| `run-activity` | Triggers the execution of a workflow activity. | Yes, a UUID representing the current workflow generation. |

> Note that all reminder names are suffixed with a series of random characters. For example, the `start` reminder might actually be named `start-149eb437`. This is because multiple reminders with the same name can result in unexpected behavior.

Each reminder is created by default with a 1-minute period. If a workflow or activity execution fails unexpectedly, it will be retried automatically after the 1-minute period expires. If the workflow or activity executions succeeds, then the reminder will be immediately deleted.