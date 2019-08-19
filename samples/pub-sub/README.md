# Actions Pub-Sub Sample

In this sample, we'll create publisher microservices and subscriber microservices to demonstrate how Actions enables a publish-subcribe pattern. Publishers will generate messages of a specific topic, while subscribers will listen for messages of specific topics. See [Why Pub-Sub](<INSERTLINK>) to understand when this pattern might be a good choice for your software architecture.

This sample includes two publishers:

- Node.js message generator
- React front-end message generator

And two subscribers: 
 
- Node.js subscriber
- Python subscriber

Further, Actions uses Redis streams (enabled in Redis versions > 5) as a message bus. The following architecture diagram illustrates how components interconnect:

<<INSERT ARCHITECTURE DIAGRAM>>

Actions allows us to deploy the same microservices from our local machines to the cloud. Correspondingly, this sample has instructions for deploying this project [locally](<<INSERT LINK>>) or in [Kubernetes](<<INSERT LINK>>). 

## Prerequisites

### Prerequisites to Run Locally

- Actions CLI with Actions initialized: <<INSERT DOCUMENTATION LINK>>
- Postman

### Prerequisites to run in Kubernetes

- Actions enabled Kubernetes cluster: <<INSERT DOCUMENTATION LINK>>

## Run Locally

In order to run the pub/sub sample locally, we'll need to run 


## Run in Kubernetes

### Setting up a State Store

## How it Works

## Why Pub-Sub?