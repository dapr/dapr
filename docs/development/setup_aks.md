
# Set Up an Azure Kubernetes Service Cluster

## Prerequisites

Install
- Install Docker
- Install [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/)
- If on Windows,
  - Virtualization needs to be enabled in BIOS
  - Hyper-V needs to be (enabled)[https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/quick-start/enable-hyper-v]
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli?view=azure-cli-latest)

## Deploy an Azure Kubernetes Service Cluster
To set up an AKS cluster, please follow these instructions:
https://docs.microsoft.com/en-us/azure/aks/kubernetes-walkthrough

In addition, make sure the cluster is RBAC enabled.

When finished run the following:
```
az aks get-credentials -n <cluster-name> -g <resource-group>
```

It should be pointing at your AKS cluster:
```
kubectl cluster-info
```




## Install Helm and Deploy Tiller

1. Install Helm if you haven't already.

Linux/macOS:
```
sudo snap install helm --classic
```

Windows:

See this: https://helm.sh/docs/using_helm/#from-chocolatey-or-scoop-windows


Create the tiller service account
```
kubectl apply -f https://raw.githubusercontent.com/Azure/helm-charts/master/docs/prerequisities/helm-rbac-config.yaml
```

2. Run the following to install tiller into the cluster
```
helm init --service-account tiller --history-max 200
```

3. To confirm the step above worked, you should see a pod with a name starting with "tiller-deploy", for example "tiller-deploy-7695cecfb7-dln4h"
```
kubectl get pods -n kube-system
```

If you do *not* see one, try running these two commands:
```
kubectl create serviceaccount -n kube-system tiller
kubectl create clusterrolebinding tiller-cluster-rule --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
```

Then repeat step 2.

4. If you still don't see a tiller pod, you may have to debug your environment.  Try running the following, and look for errors under "Events" at the bottom of the output:
```
kubectl describe deployment tiller-deploy --namespace kube-system
```

Your cluster is now ready!  Please see [Modify Actions and Deploy to the Cluster](./edit_and_deploy.md) to deploy a change to the cluster.