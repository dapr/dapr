
## Modify Actions and Deploy to the Cluster

This guide will walk through the steps to make a small change to Actions, then deploy the change to your cluster.

This guide assumes you have a local or AKS cluster set up.  If you haven't done this, follow the appropriate link:
- [Local Minikube cluster](./setup_minikube.md)
- [AKS cluster](/setup_aks.md)

1. If you haven't already cloned the code, clone https://github.com/actionscore/actions.

2. Open the following file and add a log statement of your choice in main().  We'll look for this log statement after we deploy our changes to confirm everything worked.
```
[location you cloned to]/actionscore/actions/cmd/operator/main.go
```

Save the file.

3.  Go up two levels to 
```
[location you cloned to]/actionscore/actions
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

5. Open the Dockerfile in the current directory and replace the last line, the `ADD`, with:

```
ADD ./dist/linux_amd64/release/. /
```

For Windows, use the following instead:
```
ADD dist/linux_amd64/release/. /
```

6. Now set the following environment varariables, which will be used by make:
- `ACTIONS_REGISTRY` should be set to docker.io/[your Docker Hub account name].
- `ACTIONS_TAG` should be set to whatever value you wish to use for a container image tag.


Linux/macOS:
```
export ACTIONS_REGISTRY=docker.io/[your Docker Hub account name]
export ACTIONS_TAG=dev1
```

Windows:

```
set ACTIONS_REGISTRY=docker.io/[your Docker Hub account name]
set ACTIONS_TAG=dev1
```

## Building the Container Image
From
```
[location you cloned to]/actionscore/actions
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
- `ACTIONS_REGISTRY1=docker.io/user123` 
- `ACTIONS_TAG=dev`

The command above will create an image with repo `user123/actions` (note `actions` is added) and tag `dev`.

You should see the new image if you run:
```
docker images
```
## Push the Container Image

To push the image to DockerHub, run:
```
make docker-push
```

## Deploy Actions With Your Changes
Now we'll deploy Actions with your changes.

If you deployed Actions to your cluster before, delete it now using:
```
helm del --purge actions
```

Then go to 

```
[place you cloned to]/actionscore/actions/charts/actions-operator
```

and run the following to deploy:
```
helm install --name=actions --namespace=actions-system --set-string global.registry=docker.io/[your Docker Hub id],global.tag=[the tag of the image you just built] .
```

For the settings above, here's an example with the variables filled in:
```
helm install --name=actions --namespace=actions-system --set global.registry=docker.io/user123,global.tag=dev .
```

## Verifying your changes

Once Actions is deployed, you should see the print statement you added by running:
```
kubectl logs [your actions-operator pod name]
```

For example:
```
kubectl logs actions-operator-123-456
```
 