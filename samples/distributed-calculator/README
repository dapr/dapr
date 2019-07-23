# Kubernetes Distributed Calculator

This sample demonstrates a distributed calculator: each operation is powered by a different service written in a different language/framework:

- Go [mux](https://github.com/gorilla/mux) app for addition
- Python [flask](https://flask.palletsprojects.com/en/1.0.x/) app for subtraction
- Node [Express](https://expressjs.com/) application for division
- [.NET Core](https://docs.microsoft.com/en-us/dotnet/core/) application for multiplication

The front-end application consists of a server and a client written in [React](https://reactjs.org/). 
Kudos to [ahfarmer](https://github.com/ahfarmer) whose [React calculator](https://github.com/ahfarmer/calculator) 
sample was used for the client.

Each application has been dockerized and pushed into [dockerhub](https://hub.docker.com/u/actionsdemoes), where we will pull them from.

## Running the Sample

In order to run this sample, we need to deploy all of its resources. 

1. Navigate to the deploy directory in this sample directory: `cd deploy`
2. Deploy the React front-end: `kubectl apply -f react-calculator`
3. Deploy each of the supporting applications:

```bash
kubectl apply -f actionsdemoes/go-adder.yaml
kubectl apply -f actionsdemoes/dotnet-subtractor.yaml
kubectl apply -f actionsdemoes/python-multiplier.yaml
kubectl apply -f actionsdemoes/node-divider.yaml
```
Each of these deployments will spin up a pod with two containers: one for your service and the other for the actions sidecar. It will also configure
a service for each sidecar, along with an external IP for our front-end. For more details on how these resources are spun up, look at each individual 
app's README.

4. Wait until your pods are in a running state: `kubectl get pods -w`

```bash

NAME                                    READY     STATUS    RESTARTS   AGE
actions-assigner-5c5bfb956f-ppgqr       1/1       Running   0          5d
actions-controller-b9fc5578b-htxsm      1/1       Running   0          5d
addapp-db749bff9-kpkn6                  2/2       Running   0          2m
calculator-front-end-7c549cc84d-m24cb   2/2       Running   0          3m
divideapp-6d85b88cb4-vh7nz              2/2       Running   0          1m
multiplyapp-746588586f-kxpx4            2/2       Running   0          1m
subtractapp-7bbdfd5649-r4pxk            2/2       Running   0          2m
```

5. Next, let's take a look at our services and wait until we have an external IP configured for our front-end: `kubectl get svc -w`

```bash
NAME                          TYPE           CLUSTER-IP     EXTERNAL-IP     PORT(S)            AGE
actions-api                   ClusterIP      10.0.25.74     <none>          80/TCP             5d
actions-assigner              ClusterIP      10.0.189.88    <none>          80/TCP             5d
addapp-action                 ClusterIP      10.0.1.170     <none>          80/TCP,50001/TCP   2m
calculator-front-end          LoadBalancer   10.0.155.131   40.80.152.125   80:32633/TCP       3m
calculator-front-end-action   ClusterIP      10.0.230.219   <none>          80/TCP,50001/TCP   3m
divideapp-action              ClusterIP      10.0.240.3     <none>          80/TCP,50001/TCP   1m
kubernetes                    ClusterIP      10.0.0.1       <none>          443/TCP            33d
multiplyapp-action            ClusterIP      10.0.217.211   <none>          80/TCP,50001/TCP   1m
subtractapp-action            ClusterIP      10.0.146.253   <none>          80/TCP,50001/TCP   2m
```

Each service ending in "-action" represents your service's respective sidecars, while the "calculator-front-end" service represents the external 
load balancer for the React calculator front-end.

6. Take the external IP address for `calculator-front-end` and drop it in your browser and voilà! You have a working distributed calculator!

## The Role of Actions

This sample demonstrates how we use actions as a programming model for simplifying the development of distributed systems. In this sample, actions is
enabling polyglot programming, service discovery and simplified state management.

### Polyglot Programming

Each service in this sample is written in a different programming language, but they're used together in the same larger application. Actions itself is
langauge agnostic - none of our services have to include any dependency in order to work with actions. This empowers developers to build each service 
however they want, using the best language for the job or for a particular dev team.

### Service Discovery

Traditional services running on physical hardware tend to be fairly static: their network location and infrastructure are fixed. Moderns services are 
far more dynamic. Between autoscaling, updates, and failures, service instances and their network locations are constantly changing. Correspondingly, 
building systems with networked services comes with substantial overhead. Actions sidecars abstract away this overhead, allowing developers to focus 
on _what_ their services do instead of _where_ they're hosted or _how_ they communicate.