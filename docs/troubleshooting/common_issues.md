# Common Issues

This section will walk you through some common issues and problems.

### I don't see the Actions sidecar injected to my pod

There could be several reasons to why a sidecar will not be injected into a pod.
First, check your Deployment or Pod YAML file, and check that you have the following annotations in the right place:

Sample deployment:

<pre>
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nodeapp
  labels:
    app: node
spec:
  replicas: 1
  selector:
    matchLabels:
      app: node
  template:
    metadata:
      labels:
        app: node
      annotations:
        <b>actions.io/enabled: "true"</b>
        <b>actions.io/id: "nodeapp"</b>
        <b>actions.io/port: "3000"</b>
    spec:
      containers:
      - name: node
        image: actionscore.azurecr.io/samples/nodeapp
        ports:
        - containerPort: 3000
        imagePullPolicy: Always
</pre>

If your pod spec template is annotated correctly and you still don't see the sidecar injected, make sure Actions was deployed to the cluster before your deployment or pod were deployed.

If this is the case, restarting the pods will fix the issue.

In order to further diagnose any issue, check the logs of the Actions sidecar injector:

```
 kubectl logs -l app=actions-sidecar-injector -n actions-system
```

*Note: If you installed Actions to a different namespace, replace actions-system above with the desired namespace*

### I am unable to save state or get state

Have you installed an Actions State store in your cluster?

To check, use kubectl get a list of components:

`kubectl get components`

If there isn't a state store component, it means you need to set one up.
Visit [here](../components/redis.md) for more details.

If everything's set up correctly, make sure you got the credentials right.
Search the Actions runtime logs and look for any state store errors:

`kubectl logs <name-of-pod> actionsrt`.

### I am unable to publish and receive events

Have you installed an Actions Message Bus in your cluster?

To check, use kubectl get a list of components:

`kubectl get components`

If there isn't a pub-sub component, it means you need to set one up.
Visit [here](../components/redis.md#configuring-redis-for-pubsub) for more details.

If everything's set up correctly, make sure you got the credentials right.
Search the Actions runtime logs and look for any pub-sub errors:

`kubectl logs <name-of-pod> actionsrt`.

### The Actions Operator pod keeps crashing

Check that there's only one installation of the Actions Operator in your cluster.
Find out by running `kubectl get pods -l app=actions-operator --all-namespaces`.

If two pods appear, delete the redundant Actions installation.

### I'm getting 500 Error responses when calling Actions

This means there are some internal issue inside the Actions runtime.
To diagnose, view the logs of the sidecar:

`kubectl logs <name-of-pod> actionsrt`.

### I'm getting 404 Not Found responses when calling Actions

This means you're trying to call an Actions API endpoint that either doesn't exist or the URL is malformed.
Look at the Actions API spec [here](https://github.com/actionscore/spec) and make sure you're calling the right endpoint.

### I don't see any incoming events or calls from other services

Have you specified the port your app is listening on?
In Kubernetes, make sure the `actions.io/port` annotation is specified:

<pre>
annotations:
    actions.io/enabled: "true"
    actions.io/id: "nodeapp"
    <b>actions.io/port: "3000"</b>
</pre>

If using Actions Standalone and the Actions CLI, make sure you pass the `--app-port` flag to the `actions run` command.

### My Actions-enabled app isn't behaving correctly

The first thing to do is inspect the HTTP error code returned from the Actions API, if any.
If you still can't find the issue, try enabling `debug` log levels for the Actions runtime. See [here](logs.md) how to do so.

You might also want to look at error logs from your own process. If running on Kubernetes, find the pod containing your app, and execute the following:

`kubectl logs <pod-name> <name-of-your-container>`.

If running in Standalone mode, you should see the stderr and stdout outputs from your app displayed in the main console session.
