# Actions Pub-Sub Sample

In this sample, we'll create publisher microservices and subscriber microservices to demonstrate how Actions enables a publish-subcribe pattern. Publishers will generate messages of a specific topic, while subscribers will listen for messages of specific topics. See [Why Pub-Sub](<INSERTLINK>) to understand when this pattern might be a good choice for your software architecture.

This sample includes two publishers:

- Node.js message generator
- React front-end message generator

and two subscribers: 
 
- Node.js subscriber
- Python subscriber

Further, Actions uses Redis streams (enabled in Redis versions > 5) as a message bus. The following architecture diagram illustrates how components interconnect:

<<INSERT ARCHITECTURE DIAGRAM>>

Actions allows us to deploy the same microservices from our local machines to the cloud. Correspondingly, this sample has instructions for deploying this project [locally](<<INSERT LINK>>) or in [Kubernetes](<<INSERT LINK>>). 

## Prerequisites

### Prerequisites to Run Locally

- Actions CLI with Actions initialized: <<INSERT DOCUMENTATION LINK>>
- Node and/or Python
- Postman

### Prerequisites to Run in Kubernetes

- Actions enabled Kubernetes cluster: <<INSERT DOCUMENTATION LINK>>

## Run Locally

In order to run the pub/sub sample locally, we need to run each of our microservices with Actions. We'll start by running our messages subscribers. **Note**: These instructions deploy a Node subscriber and a Python subscriber, but if you don't have either Node or Python, feel free to run just one.

#### Run Node Message Subscriber with Actions

1. Navigate to Node subscriber directory in your CLI: `cd node-subscriber`
2. Run Node subscriber app with Actions: `actions run --app-id node-subscriber --app-port 3000 node app.js`
    
    We assign `app-id`, which we can be whatever unique identifier we like. We also assign `app-port`, which is the port that our Node application is running on. Finally, we pass the command to run our app: `node app.js`.

#### Run Python Message Subscriber with Actions

1. Open a new CLI window and navigate to Python subscriber directory in your CLI: `cd python_subscriber`
2. Run Python subscriber app with Actions: `actions run --app-id python-subscriber --app-port 3000 run python app.py`
    
    We assign `app-id`, which we can be whatever unique identifier we like. We also assign `app-port`, which is the port that our Node application is running on. Finally, we pass the command to run our app: `python app.py`.

#### Use the CLI to Publish Messages to Subscribers

The Actions CLI provides a mechanism to publish messages for testing purposes. Let's test that our subscribers are listening!

1. Run `actions publish --app-id node-subscriber --topic A --payload '{ "message": "This is a test" }'

    Note that 

## Run in Kubernetes

### Setting up a State Store

## How it Works

### Node Message Publisher

### Node Message Subscriber

### Python Message Subscriber

### React Front-End

Our React front-end was bootstrapped with [Create React App](<INSERTLINK>). 

#### Client

MessageForm component 

#### Server
Relays messages from the client to the message bus, where subscribers will pick them up.

## Why Pub-Sub?