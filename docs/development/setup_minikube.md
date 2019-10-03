
# Set Up a Minikube Cluster

## Prerequisites

Install
- Install Docker
- Install [Minikube](https://kubernetes.io/docs/tasks/tools/install-minikube/)
- Install [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/)
- If on Windows,
  - Virtualization needs to be enabled in BIOS
  - Hyper-V needs to be (enabled)[https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/quick-start/enable-hyper-v]


## Start the Minikube Cluster
1. To start the cluster, type the following:

Linux/macOS:
```
minikube start --vm-driver=kvm2 --cpus=4 --memory=4096 --kubernetes-version=1.14.6 --extra-config=apiserver.authorization-mode=RBAC
```

Windows:
```
minikube start --vm-driver=hyperv --cpus=4 --memory=4096 --kubernetes-version=1.14.6 --extra-config=apiserver.authorization-mode=RBAC
```

*Note: if you stop the cluster, you must start it again with the args above.  A common mistake is to start it again with a shortened line:*

```
[wrong] minikube start
```

2. In some environments, the args above may not configure RBAC correctly.  The following steps will set it correctly if that happens.

Create clusterrole.yaml somewhere locally with these contents.:
```
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
  name: cluster-admin
rules:
- apiGroups:
  - '*'
  resources:
  - '*'
  verbs:
  - '*'
- nonResourceURLs:
  - '*'
  verbs:
  - '*'
```

3. Now run the command below to apply it
```
kubectl create -f clusterrole.yaml
```
You may get `AlreadyExists`.  That is ok.


## Install Helm and Deploy Tiller

1. [Install Helm](https://helm.sh/docs/using_helm/#installing-the-helm-client) if you haven't already:


2. Create the tiller service account
```
kubectl apply -f https://raw.githubusercontent.com/Azure/helm-charts/master/docs/prerequisities/helm-rbac-config.yaml
```

3. Run the following to install tiller into the cluster
```
helm init --service-account tiller --history-max 200
```

4. To confirm the step above worked, you should see a pod with a name starting with "tiller-deploy", for example "tiller-deploy-7695cecfb7-dln4h"
```
kubectl get pods -n kube-system
```

If you do *not* see one, try running these two commands:
```
kubectl create serviceaccount -n kube-system tiller
kubectl create clusterrolebinding tiller-cluster-rule --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
```

Then repeat step 3.

5. If you still don't see a tiller pod, you may have to debug your environment.  Try running the following, and look for errors under "Events" at the bottom of the output:
```
kubectl describe deployment tiller-deploy --namespace kube-system
```

Your Minikube cluster is now ready!  Please see [Modify Daprs and Deploy to the Cluster](./edit_and_deploy.md) to deploy a local change to the cluster.