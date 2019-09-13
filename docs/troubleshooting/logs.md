# Logs

This section will assist you in understanding how logging works in Actions, configuring and viewing logs.

## Overview

Logs have different, configurable verbosity levels.
The levels outlined below are the same for both system components and the Actions sidecar process/container:

1. error
2. warning
3. info
3. debug

error produces the minimum amount of output, where debug produces the maximum amount. The default level is info, which provides a balanced amount of information for operating Actions in normal conditions.

To set the output level, you can use the `--log-level` command-line option. For example:

`./actionsrt --log-level error` <br>
`./placement --log-level debug`

This will start the Actions runtime binary with a log level of `error` and the Actions Actor Placement Service with a log level of `debug`.

## Configuring logs on Standalone Mode

As outlined above, every Actions binary takes a `--log-level` argument. For example, to launch the placement service with a log level of warning:

`./placement --log-level warning`

To set the log level when running your app with the Actions CLI, pass the `log-level` param:

`actions run --log-level warning node myapp.js`

## Viewing Logs on Standalone Mode

When running Actions with the Actions CLI, both your app's log output and the runtime's output will be redirected to the same session, for easy debugging.
For example, this is the output when running Actions:

```
actions run node myapp.js
ℹ️  Starting Actions with id Trackgreat-Lancer on port 56730
✅  You're up and running! Both Actions and your app logs will appear here.

== APP == App listening on port 3000!
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="starting Actions Runtime -- version 0.3.0-alpha -- commit b6f2810-dirty"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="log level set to: info"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="standalone mode configured"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="action id: Trackgreat-Lancer"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="loaded component statestore (state.redis)"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="loaded component messagebus (pubsub.redis)"
== ACTIONS == 2019/09/05 12:26:43 redis: connecting to localhost:6379
== ACTIONS == 2019/09/05 12:26:43 redis: connected to localhost:6379 (localAddr: [::1]:56734, remAddr: [::1]:6379)
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=warning msg="failed to init input bindings: app channel not initialized"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="actor runtime started. actor idle timeout: 1h0m0s. actor scan interval: 30s"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="actors: starting connection attempt to placement service at localhost:50005"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="http server is running on port 56730"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="gRPC server is running on port 56731"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="actions initialized. Status: Running. Init Elapsed 8.772922000000001ms"
== ACTIONS == time="2019-09-05T12:26:43-07:00" level=info msg="actors: established connection to placement service at localhost:50005"
```

## Configuring Logs on Kubernetes

This section shows you how to configure the log levels for Actions system pods and the Actions sidecar running on Kubernetes.

### Setting the sidecar log level

You can set the log level individually for every sidecar by providing the following annotation in your pod spec template:

```
annotations:
  actions.io/log-level: "debug"
```

### Setting system pods log level

When deploying Actions to your cluster using Helm, you can individually set the log level for every Actions system component.

#### Setting the Operator log level

`helm install actionscore/actions-operator -n actions --namespace actions-system --set actions_operator.logLevel=error`

#### Setting the Placement Service log level

`helm install actionscore/actions-placement -n actions --namespace actions-system --set actions_placement.logLevel=error`

#### Setting the Sidecar Injector log level

`helm install actionscore/actions-sidecar-injector -n actions --namespace actions-system --set actions_sidecar_injector.logLevel=error`



## Viewing Logs on Kubernetes

Actions logs are written to stdout and stderr.
This section will guide you on how to view logs for Actions system components as well as the Actions sidecar.

### Sidecar Logs

When deployed in Kubernetes, the Actions sidecar injector will inject an Actions container named `actionsrt` into your annotated pod.
In order to view logs for the sidecar, simply find the pod in question by running `kubectl get pods`:

```
NAME                                        READY     STATUS    RESTARTS   AGE
addapp-74b57fb78c-67zm6                     2/2       Running   0          40h
```

Next, get the logs for the Actions sidecar container:

`kubectl logs addapp-74b57fb78c-67zm6 -c actionsrt`
```
time="2019-09-04T02:52:27Z" level=info msg="starting Actions Runtime -- version 0.3.0-alpha -- commit b6f2810-dirty"
time="2019-09-04T02:52:27Z" level=info msg="log level set to: info"
time="2019-09-04T02:52:27Z" level=info msg="kubernetes mode configured"
time="2019-09-04T02:52:27Z" level=info msg="action id: addapp"
time="2019-09-04T02:52:27Z" level=info msg="application protocol: http. waiting on port 6000"
time="2019-09-04T02:52:27Z" level=info msg="application discovered on port 6000"
time="2019-09-04T02:52:27Z" level=info msg="actor runtime started. actor idle timeout: 1h0m0s. actor scan interval: 30s"
time="2019-09-04T02:52:27Z" level=info msg="actors: starting connection attempt to placement service at actions-placement.actions-system.svc.cluster.local:80"
time="2019-09-04T02:52:27Z" level=info msg="http server is running on port 3500"
time="2019-09-04T02:52:27Z" level=info msg="gRPC server is running on port 50001"
time="2019-09-04T02:52:27Z" level=info msg="actions initialized. Status: Running. Init Elapsed 64.234049ms"
time="2019-09-04T02:52:27Z" level=info msg="actors: established connection to placement service at actions-placement.actions-system.svc.cluster.local:80"
```

### System Logs

Actions runs the following system pods:

* Actions operator
* Actions sidecar injector
* Actions placement service

#### Viewing Operator Logs

```
kubectl logs -l app=actions-operator -n actions-system
time="2019-09-05T19:03:43Z" level=info msg="log level set to: info"
time="2019-09-05T19:03:43Z" level=info msg="starting Actions Operator -- version 0.3.0-alpha -- commit b6f2810-dirty"
time="2019-09-05T19:03:43Z" level=info msg="Actions Operator is started"
```

*Note: If Actions is installed to a different namespace than actions-system, simply replace the namespace to the desired one in the command above*

#### Viewing Sidecar Injector Logs

```
kubectl logs -l app=actions-sidecar-injector -n actions-system
time="2019-09-03T21:01:12Z" level=info msg="log level set to: info"
time="2019-09-03T21:01:12Z" level=info msg="starting Actions Sidecar Injector -- version 0.3.0-alpha -- commit b6f2810-dirty"
time="2019-09-03T21:01:12Z" level=info msg="Sidecar injector is listening on :4000, patching Actions-enabled pods"
```

*Note: If Actions is installed to a different namespace than actions-system, simply replace the namespace to the desired one in the command above*

#### Viewing Placement Service Logs

```
kubectl logs -l app=actions-placement -n actions-system
time="2019-09-03T21:01:12Z" level=info msg="log level set to: info"
time="2019-09-03T21:01:12Z" level=info msg="starting Actions Placement Service -- version 0.3.0-alpha -- commit b6f2810-dirty"
time="2019-09-03T21:01:12Z" level=info msg="placement Service started on port 50005"
time="2019-09-04T00:21:57Z" level=info msg="host added: 10.244.1.89"
```
*Note: If Actions is installed to a different namespace than actions-system, simply replace the namespace to the desired one in the command above*

### Non Kubernetes Environments

The examples above are specific specific to Kubernetes, but the principal is the same for any kind of container based environment: simply grab the container ID of the Actions sidecar and/or system component (if applicable) and view its logs.

