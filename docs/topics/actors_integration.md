# Integration with Actor Frameworks
This article introduces how Actions integrate with existing Actor Frameworks such as [Orleans](https://github.com/dotnet/orleans), [Akka](https://akka.io/), and [Service Fabric Reliable Actors](https://docs.microsoft.com/en-us/azure/service-fabric/service-fabric-reliable-actors-introduction). With Actions integration, these Actor frameworks will allow developers to write Actor code in any programming languages, and to plug their Actors into Actions eventing pipeline that enables reliable messaging, pub-sub, binding to event sources and many other features.

## Client-side integration
On the cliend side, Actions acts as a shim in front of an Actor proxy. This allows Actions to bring reliable messaging, binding and other features to the connected Actor framework.
![Actors client](../imgs/actors_client.png)

## Server-side integration
On the server side, Actions leverages Actor framework capabilities such as state management and single activation guarantee to offer complete Actor framework capabilities. Actions defines an **Actor API**, which the integrated Actor frameworks are supposed to implement/support. 

> Actions comes with a built-in Actor framework implemenation through the same Actor API.

The following diagram illustrates two possible execution paths. ![Actors server](../imgs/actors_server.png)

### Client calling an Actor (green path)

1. A client makes a call to an Actor instance.
2. Actions calls an Actor proxy to forward the request.
3. As the Actor runtime activates/invokes an Actor, it calls Actions through the Actor API to invoke the user method.
4. Actions forwards the request to user code through Actions API.
5. As the response is returned, it was sent back to client as a response to the original request. 

### A message triggering an Actor (yellow path)

1. A message is routed to the Actions instance that is hosting the destination Actor.
2. Actions dispatches the message to the Actor runtime when possible.
3. The rest of the steps are the same as the green path.

## Actor API
At a high level, the Actor API shall contain the following methods:

* Dispatch to Actor when possible
* Call method on an Actor now
* Load/save state
* Create/remove
* Locate & send message to an Actor 

## Custom Programming Experience
Custom programming experiences can be built on top of Actions through a custom library. The Custom library adapts Actions API to a custom API shape that the user code consumes, as shown in the following diagram:
![custom_experience](../imgs/programming_experience.png)