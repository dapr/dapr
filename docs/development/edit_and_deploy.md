
## Developing Dapr and Deploying to a Cluster

This guide will walk through the steps to make a small change to Dapr, then deploy the change to your cluster.

This guide assumes you have a local or AKS cluster set up.  If you haven't done this, follow the appropriate link:
- [Local Minikube cluster](./setup_minikube.md)
- [AKS cluster](/setup_aks.md)

1. If you haven't already cloned the code, clone https://github.com/dapr/dapr.

2. Open the following file and add a log statement of your choice in main().  We'll look for this log statement after we deploy our changes to confirm everything worked.
```
[location you cloned to]/dapr/dapr/cmd/operator/main.go
```

Save the file.

3.  Go up two levels to 
```
[location you cloned to]/dapr/dapr
```

and build.  We're going to build for a Linux container.

For Linux or macOS:
```
make build GOOS=linux GOARCH=amd64
```

For Windows:
```
mingw32-make.exe build GOOS=linux GOARCH=amd64
```

For all environments, you should see the built binaries in ./dist/linux_amd64/release.

4. We're going to now build a container with our changes and push it to Docker Hub.  If you haven't created a Docker Hub account yet, create one now at https://hub.docker.com.

After creating the account, log into Docker Hub:
```
docker login
```

5. Now set the following environment varariables, which will be used by make:
- `DAPR_REGISTRY` should be set to docker.io/[your Docker Hub account name].
- `DAPR_TAG` should be set to whatever value you wish to use for a container image tag.


Linux/macOS:
```
export DAPR_REGISTRY=docker.io/[your Docker Hub account name]
export DAPR_TAG=dev1
```

Windows:

```
set DAPR_REGISTRY=docker.io/[your Docker Hub account name]
set DAPR_TAG=dev1
```

## Building the Container Image
From
```
[location you cloned to]/daprscore/dapr
```

Run the appropriate command below to build the container image.

For Linux/macOS, you'll have to configure docker to run without sudo for this to work, because of the environment variables.  See the following on how to configure this:  https://docs.docker.com/install/linux/linux-postinstall/

Linux/macOS:
```
make docker-build
```
Windows:
```
mingw32-make.exe docker-build
```

For example, if our environment variables are set like so:
- `DAPR_REGISTRY=docker.io/user123` 
- `DAPR_TAG=dev`

The command above will create an image with repo `user123/dapr` (note `dapr` is added) and tag `dev`.

You should see the new image if you run:
```
docker images
```
## Push the Container Image

To push the image to DockerHub, run:

Linux/macOS:
```
make docker-push
```
Windows:
```
mingw32-make.exe docker-push
```

## Deploy Dapr With Your Changes
Now we'll deploy Dapr with your changes.

If you deployed Dapr to your cluster before, delete it now using:
```
helm del --purge dapr
```

Then go to 

```
<repo root>/dapr/dapr/charts/dapr-operator
```

and run the following to deploy:

Linux/macOS:
```
make docker-deploy-k8s
```
Windows:
```
mingw32-make.exe docker-deploy-k8s
```

## Verifying your changes

Once Dapr is deployed, print the Dapr pods:

```
kubectl.exe get po -n dapr-system

```

Find the one with a name starting with dapr-operator (e.g. dapr-operator-123-456), and run

```
kubectl logs [your dapr-operator pod name]
```

you should see the print statement you added.

 