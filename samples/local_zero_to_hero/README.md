# From Zero to Hero Locally

This tutorial will demonstrate how to get Actions running locally on your machine. We'll be deploying a Node.js app that subscribes to order messages and persists them.

By the end of the end, you will know how to:

1. Set up Actions Locally
2. Understand the Code
3. Run the Node.js app with Actions
4. Post Messages to your Service
5. Confirm Successful Persistence

## Prerequisites
This sample requires you to have the following installed on your machine:
- [Docker](https://docs.docker.com/)
- [Node](https://nodejs.org/en/)
- [Postman](https://www.getpostman.com/)

## Step 1 - Setup Actions 

1. Install actions as standalone, following [these instructions](https://github.com/actionscore/actions#install-as-standalone).
2. Download the [Actions CLI release](https://github.com/actionscore/cli/releases) for your OS

    **Note for Windows Users**: Due to a known bug, you must rename 'action' to 'actions.exe'

3. Add the paths to Actions and the Actions CLI to your PATH
4. Run `actions init`, which will set up create two containers: the actions runtime and a redis state store. To validate that these two containers were successfully created, run `docker ps` and observe output: 
```
CONTAINER ID        IMAGE                   COMMAND                  CREATED             STATUS              PORTS                     NAMES
84b19574f5e5        actionscore.azurecr.io/actions:latest   "./placement"             About an hour ago   Up About an hour    0.0.0.0:6050->50005/tcp   xenodochial_chatterjee
78d39ae67a95        redis                   "docker-entrypoint.s…"   About an hour ago   Up About an hour    0.0.0.0:6379->6379/tcp    hungry_dubinsky
```
5. Clone actions repo: `git clone https://github.com/actionscore/actions.git`

## Step 2 - Understand the Code

Now that we've locally set up actions and cloned the repo, let's take a look at our local zero-to-hero sample. Navigate to the local_zero_to_hero sample: `cd samples/local_zero_to_hero/app.js`.

In the `app.js` you'll find a simple `express` application, which exposes a few routes and handlers. First, let's take a look at the `actionsUrl` at the top of the file: 

```js
const actionsUrl = `http://localhost:${process.env.ACTIONS_PORT}`;
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

**Note**: If we only expected to have a single instance of the Node app, and didn't expect anything else to update "order", we instead could have kept a local version of our order state and returned that (reducing a call to our Redis store). We would then create a `/state` POST endpoint, which would allow actions to initialize our app's state when it starts up. In that case, our Node app would be `stateful`.

## Step 3 - Run the Node.js App with Actions

1. Navigate to the zero to hero node sample project: `cd samples/local_zero_to_hero/app.js`

2. Install dependencies: `npm install`. This will install `express` and `body-parser`

3. Run node application with actions: `actions run --port 3500 --app-id mynode --app-port 3000 node app.js`. This should output text that looks like the following, along with logs:

```
Starting Actions with id mynode on port 3500
You're up and running! Both Actions and your app logs will appear here. 
...
```

4. Copy the Actions port for the next step

## Step 4 - Post Messages to your Service

Now that our actions and node app are running, let's post messages against it. 

 Open Postman and create a POST request against `http://localhost:<YOUR_PORT>/<YOUR_APP_NAME>/neworder`
![Postman Screenshot](./img/postman1.jpg)
In your terminal window, you should see logs indicating that the message was received and state was updated:
```bash
[0m[94;1m== APP == Got a new order! Order ID: 42
[0m[94;1m== APP == Successfully persisted state
```

## Step 5 - Confirm Successful Persistence

Now, let's just make sure that we our order was successfully persisted to our state store. Create a GET request against: `http://localhost:<YOUR_PORT>/<YOUR_APP_NAME>/order`
![Postman Screenshot 2](./img/postman2.jpg)

This invokes the `/order` route, which calls out to our Redis store for the latest data. Observe the expected result!
