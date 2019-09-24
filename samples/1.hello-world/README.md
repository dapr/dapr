# Hello World

This tutorial will demonstrate how to get Actions running locally on your machine. We'll be deploying a Node.js app that subscribes to order messages and persists them. The following architecture diagram illustrates the components that make up this sample: 

![Architecture Diagram](./img/Architecture_Diagram.jpg)

## Prerequisites
This sample requires you to have the following installed on your machine:
- [Docker](https://docs.docker.com/)
- [Node.js version 8 or greater](https://nodejs.org/en/) 
- [Postman](https://www.getpostman.com/) [Optional]

## Step 1 - Setup Actions 

Follow [instructions](https://github.com/actionscore/actions#install-as-standalone) to download the Actions CLI and initialize Actions.

## Step 2 - Understand the Code

Now that we've locally set up actions and cloned the repo, let's navigate to the Hello World sample: `cd samples/1.hello-world`.

In the `app.js` you'll find a simple `express` application, which exposes a few routes and handlers. First, let's take a look at the `stateUrl` at the top of the file: 

```js
const stateUrl = `http://localhost:${process.env.ACTIONS_PORT}/v1.0/state`;
```
When we use the Actions CLI, it creates an environment variable for the Actions port, which defaults to 3500. We'll be using this in step 3 when we POST messages to to our system.

Next, let's take a look at the ```neworder``` handler:

```js
app.post('/neworder', (req, res) => {
    const data = req.body.data;
    const orderId = data.orderId;
    console.log("Got a new order! Order ID: " + orderId);

    const state = [{
        key: "order",
        value: data
    }];

    fetch(stateUrl, {
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

This calls out to our Redis cache to grab the latest value of the "order" key, which effectively allows our Node.js app to be _stateless_. 

> **Note**: If we only expected to have a single instance of the Node.js app, and didn't expect anything else to update "order", we instead could have kept a local version of our order state and returned that (reducing a call to our Redis store). We would then create a `/state` POST endpoint, which would allow actions to initialize our app's state when it starts up. In that case, our Node.js app would be _stateful_.

## Step 3 - Run the Node.js App with Actions

1. Install dependencies: `npm install`. This will install `express` and `body-parser`, dependencies that are shown in our `package.json`.

2. Run Node.js app with Actions: `actions run --app-id mynode --app-port 3000 --port 3500 node app.js`. This should output text that looks like the following, along with logs:

```
Starting Actions with id mynode on port 3500
You're up and running! Both Actions and your app logs will appear here.
...
```
> **Note**:  The Actions `--port` parameter with the `run` command is optional, and if not supplied, a random available port is used.

## Step 4 - Post Messages to your Service

Now that Actions and our Node.js app are running, let's POST messages against it.

You can do this using `curl` with:

```sh
curl -XPOST -d @sample.json http://localhost:3500/v1.0/invoke/mynode/method/neworder
```

Or you can use the Postman GUI

Open Postman and create a POST request against `http://localhost:3500/v1.0/invoke/mynode/method/neworder`
![Postman Screenshot](./img/postman1.jpg)
In your terminal window, you should see logs indicating that the message was received and state was updated:
```bash
== APP == Got a new order! Order ID: 42
== APP == Successfully persisted state
```

## Step 5 - Confirm Successful Persistence

Now, let's just make sure that our order was successfully persisted to our state store. Create a GET request against: `http://localhost:3500/v1.0/invoke/mynode/method/order`

```sh
curl http://localhost:3500/v1.0/invoke/mynode/method/order
```

Or use the Postman GUI

![Postman Screenshot 2](./img/postman2.jpg)

This invokes the `/order` route, which calls out to our Redis store for the latest data. Observe the expected result!

## Step 6 - Cleanup

To stop your services from running, simply stop the "actions run" process. Alternatively, you can spin down each of your services with the Actions CLI "stop" command. For example, to spin down your Node service, run: 

```bash
actions stop --app-id mynode
```

To see that services have stopped running, run `actions list`, noting that your service no longer appears!

## Next Steps

Now that you've gotten Actions running locally on your machine, see the [Hello Kubernetes](../2.hello-kubernetes) to get set up in Kubernetes!
