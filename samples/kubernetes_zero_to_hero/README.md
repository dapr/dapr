# From Zero to Hero with Kubernetes

This tutorial will get you up and running with Actions in a Kubernetes cluster. We'll be deploying a python app that generates messages and a Node app that consumes and persists them. 

By the end of this tutorial, you will know how to:

1. Set up Actions on your Kubernetes Cluster
2. Understand the Code
3. Deploy the Node App with the Actions Sidecar
4. Deploy the Python App with the Actions Sidecar
5. Observe Messages
6. Confirm Successful Persistence

## Step 1 - Setup Actions on your Kubernetes Cluster

The first thing you need is an RBAC enabled Kubernetes cluster. This could be running on your machine using Minikube, or it could be a fully-fledged cluser in Azure using [AKS](https://azure.microsoft.com/en-us/services/kubernetes-service/).

Next, follow [these steps](/../README.md#Install-on-Kubernetes) to have Actions deployed to your Kubernetes cluster.<br>

Finally, we'll also want to go set up a state store on our cluster. Follow [these instructions](../concepts/state/redis.md) to set up a Redis store.

## Step 2 - Understand the Code

Now that we have everything we need, let's take a look at our services. First, let's look at the node app. Navigate to the Node app in the Kubernetes sample: `cd samples/kubernetes_zero_to_hero/node.js/app.js`.

In the `app.js` you'll find a simple `express` application, which exposes a few routes and handlers.

Let's take a look at the ```neworder``` handler:

```js
app.post('/neworder', (req, res) => {
    const data = req.body.data;
    const orderId = data.orderId;
    console.log("Got a new order! Order ID: " + orderId);

    const state = [{
        key: "order",
        value: data
    }];

    fetch(`${actionsUrl}/state`, {
        method: "POST",
        body: JSON.stringify(state),
        headers: {
            "Content-Type": "application/json"
        }
    }).then((response) => {
        console.log((response.ok) ? "Successfully persisted state" : "Failed to persist state");
    });

    res.status(200).send();
});
```

Here we're exposing an endpoint that will receive and handle `neworder` messages. We first log the incoming message, and then persist the order ID to our Redis store by posting a state array to the `/state` endpoint.

Alternatively, we could have persisted our state by simply returning it with our response object:

```js
res.json({
        state: [{
            key: "order",
            value: order
        }]
    })
```

We chose to avoid this approach, as it doesn't allow us to verify if our message successfully persisted.

We also expose a GET endpoint, `/order`:

```js
app.get('/order', (_req, res) => {
    fetch(`${actionsUrl}/state/order`)
        .then((response) => {
            return response.json();
        }).then((orders) => {
            res.send(orders);
        });
});
```

This calls out to our Redis cache to grab the latest value of the "order" key, which effectively allows our node app to be _stateless_. 

## Step 3 - Deploy the Node App with the Actions Sidecar

```
kubectl apply -f ./deploy/node.yaml
```

This will deploy our web app to Kubernetes. **NOTE**: While the dockerhub repository is private, you will only be able to deploy images by creating a secret. You can do this by executing: 

```bash
kubectl create secret docker-registry actions-core-auth --docker-server https://index.docker.io/v1/ --docker-username <YOUR_USERNAME> --docker-password <YOUR_PASSWORD> --docker-email <YOUR_EMAIL>
```

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

## Step 4 - Deploy the Python App with the Actions Sidecar
Next, let's take a quick look at our python app. Navigate to the python app in the kubernetes sample: `cd samples/kubernetes_zero_to_hero/python/app.py`.

At a quick glance, this is a basic python app that posts JSON message to ```localhost:3500```, which is the default listening port for Actions. We invoke our node application's `neworder` endpoint by posting to `/action/nodeapp/neworder`. Our message contains some `data` with an orderId that increments once per second. 

```python
actions_url = "http://localhost:3500/action/nodeapp/neworder"
n = 0
while True:
    n += 1
    message = {"data": {"orderId": n}}

    try:
        response = requests.post(actions_url, json=message)
    except Exception as e:
        print(e)

    time.sleep(1)
```

Let's deploy the python app to your Kubernetes cluster:
```
kubectl apply -f ./deploy/python.yaml
```

Now, let's just wait for the pod to be in ```Running``` state:

```
kubectl get pods --selector=app=python -w
```

## Step 5 - Observe Messages

Now that we have our node and python applications deployed, let's watch messages come through.<br>
Get the logs of our node app:

```
kubectl logs --selector=app=node -c node
```

If all went well, you should see logs like this:

```
Got a new order! Order ID: 1
Successfully persisted state
Got a new order! Order ID: 2
Successfully persisted state
Got a new order! Order ID: 3
Successfully persisted state
```

## Step 6 - Confirm Successful Persistence

Hit the node app's order endpoint to get the latest order. Grab the external IP address that we saved before and, append "/order" and perform a GET request against it (enter it into your browser, use Postman, or curl it!):

```
curl $NODE_APP/order
{"orderID":"42"}
```

You should see the latest JSON in response!
