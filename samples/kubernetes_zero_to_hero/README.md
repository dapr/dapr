# From Zero to Hero with Kubernetes

This tutorial will get you up and running with Actions in no time.
By the end of this tutorial, you will know how to:

1. Set up Actions on your Kubernetes cluster
2. Deploy an Actions enabled Kubernetes Pod
3. Publish and receive messages from Node.js and Python
4. Save and restore Action state
5. Bonus - get your code triggered by external Event Sources

In this tutorial, we'll be deploying a node.js app that subscribes to messages arriving on ```neworder``` and saving it's state.
We'll also be deploying a Python app that publishes a new message.

Let's get going!

## Step 1 - Setup

First thing you need is an RBAC enabled Kubernetes cluster.
Follow the steps [here](../../../README.md#Install-on-Kubernetes) to have Actions deployed to your Kubernetes cluster.<br>

As we'll be deploying stateful apps, you'll also need to set up a state store.
You can find the instructions [here](../../state/redis.md).

## Step 2 - Deploy the node.js code with the Actions sidecar

Take a look at the node app at ```/docs/getting_started/zero_to_hero/node.js/app.js```.

There are a few things of interest here: first, this is a very simple express application, which exposes a few routes and handlers.

Take a look at the ```neworder``` handler:

```
app.post('/neworder', (req, res) => {
    data = req.body.data
    orderID = data.orderID

    console.log("Got a new order! Order ID: " + orderID)

    order = data
    
    res.json({
        state: [
            {
                key: "order",
                value: order
            }
        ]
    })
})
```

As you can see, in order to register for an event, you only need to listen on some event name.
That event name can be used by other Actions to send messages to, or it can be the name of an Event Source you defined, for example [Azure Event Hubs](../../azure_eventhubs.md).<br><br>

But the Action doesn't stop there!
We are returning a JSON response to Actions saying we want to save a state in a key-value format:

```
res.json({
        state: {
            key: "order",
            value: order
        }
    })
```

All the heavy lifting, retries, concurrency handling etc. is handled by our invisible friend, Action.

Now that we save our state, we want to get it as soon as our process launches, so we can either reject the state and start clean or accept it.
To do that, simply listen on a POST ```/state``` endpoint:

```
app.post('/state', (req, res) => {
    e = req.body

    if (e.length > 0) {
        order = e[0].value
    }

    res.status(200).send()
})
```

Here, we are simply assigning the first item of the state array back to our order value.

This is enough, lets deploy our app:

```
kubectl apply -f ./deploy/node.yaml
```

This will deploy our web app to Kubernetes.
The Actions control plane will automatically inject the Actions sidecar to our Pod.

If you take a look at the ```node.yaml``` file, you will see how Actions is enabled for that deployment:

```actions.io/enabled: true``` - this tells the Action control plane to inject a sidecar to this deployment.
```actions.io/id: nodeapp``` - this assigns a unique id or name to the Action, so it can be sent messages to and communicated with by other Actions.


This deployment provisions an External IP.
Wait until the IP is visible: (may take a few minutes)

```
kubectl get svc nodeapp
```

Once you have an external IP, save it.
You can also export it to a variable:

```
export NODE_APP=$(kubectl get svc nodeapp --output 'jsonpath={.status.loadBalancer.ingress[0].ip}')
```

## Step 3 - Deploy the Python app with the Actions sidecar

```
kubectl apply -f ./deploy/python.yaml
```

Our Python app will be used to publish a message every second.

If you look at /python/app.py, you will notice it sends a JSON message to the actions url at ```localhost:3500```, the default listening endpoint for Actions.

```
while True:
  message = "{\"data\":{\"orderID\":\"777\"}", "\"eventName\": \"neworder\"}"

  try:
    response = requests.post(actions_url, data=message)
  except Exception:
      pass
```

Wait for the pod to be in ```Running``` state:

```
kubectl get pods --selector=app=python -w
```

## Step 4 - Observe messages coming through and rejoice

Great, we now have both our apps deployed along with the Actions sidecars.<br>
Get the logs of our node app:

```
kubectl logs --selector=app=node -c node
```

If everything went right, you should be seeing something like this in the logs:

```
Got a new order! Order ID: 777
```

## Step 5 - Confirm our immortality (aka State)

Hit the node app's order endpoint to get the latest order.
Remember that IP from before? put it in your browser, or curl it:

```
curl $NODE_APP/order
{"orderID":"777"}
```

You should be getting the order JSON as a response.
Now, we'll scale the Python app to zero so it stops sending messages:

```
kubectl scale deploy pythonapp --replicas 0
```

Wait until all the pods have terminated:

```
kubectl get pods --selector=app=python
```

Delete the node app pod, and wait for it to come back up:

```
kubectl delete pod --selector=app=node
kubectl get pod --selector=app=node -w
```

Hit the order endpoint again, and voila! our state has been restored.
