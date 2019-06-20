# Actor Pattern with Actions

Actions is a new programming model that is based on RPC communication between the framework and the user code, namely HTTP. However, it does support developers to write their code using the Actor Pattern. Specifically, Actions allow a contextual Id to be associated with messages. Actions provides:

1) Routing by a contextual Id.
2) Ensuring single-threaded access per contextual Id.
3) Setting and getting state by a contextual Id is serialized.

The combination of above behaviors delivers essential characteristics of an Actor: uniquely identified, single-threaded, with isolated state.

## Authoring an Actor

In Actions, authoring Actor code is no difference with writing any other service code. An Actor in this case is a web server that listens to any number of messages the actor expects to handle.

When a request is routed to a handler of the web server, a contextual Id may present in the request header. Actions guarantees that requests with the same contextual Id are dispatched to the web server sequentially. 

Hanlder code can set or retrieve state from the Actions sidecar with the contextual Id attached.

## Talking to an Actor

Just like invoking any other Actions instances, you can send a request to an Actor by doing a POST to the sidecar. The **to** address of your message will be in the format of:
```yaml
<Actions name>.<Contextual Id>
```

> Actions uses **virtual actors** pattern. You don't need to explicitly create or destroy Actor instances. 

For example, the following POST request to **/set-temperature** sends a temperature payload to a **theromstat** with id **123**:
```json
{
    "to": [
        "theromstat.123"
    ],
    "data": {
        "temperature": 72
    }
}
```

Your code can access the response from the actor through the HTTP response body.

Actions also allows you to make direct calls to actors. The Actions sidecar provides a special **/actor** route that can be used to route requests to actor instance. For example, to invoke the above actor directly, send a POST request to:
```bash
http://localhost:<sidecarport>/actors/theromstat/123
```

## Key Differences From a Full Actor Framework

Actions programming model is not an Actor programming model. Although it provides certain actor characteristics, Actions differ from common Actor programming model in several ways.

### Single Activation

Many Actor frameworks requires single activation of an actor at any time. Actions doesn’t offer such guarantees. There should be a single activation for the most of time. However, multiple activations may exist during failovers, scaling, and network partitions. Actions will converge back to single activation when such conditions are resolved. 

Actions offers exact-once delivery within a configurable window. Actions delivers requests for the same actor id to the same service instance, while serializing client requests as well as state access of an instance. The combination of these features offers a high-fidelity simulation of the single activation behavior. However, there could be corner cases that cause problems when multiple activations do occur. 

### State Transaction Scope

Some Actor frameworks allow wrapping multiple state operations into an atomic transaction. In Actions, each state operation is a separate transaction. Because Actions doesn't dictate how user code is written, a user may trigger multiple state transactions in her code. If the code crashes between transactions, the state is left at the last committed transaction.


## More Information
To learn more about Actions Actor Pattern support, consult the following topics:

* [Enabling the Actor Pattern](../../topics/enable_actor_pattern.md)