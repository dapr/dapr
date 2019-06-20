# HTTP Event Source

This tutorial shows you how to setup an HTTP event source to read or write data to/from.

The HTTP Event Source can be used to send or read data from any HTTP system, such as Azure Functions, AWS Lambda etc.

## Create the configuration file

Create a file called http.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: <NAME>
spec:
  type: http
  connectionInfo:
    url: "<URL>"
    method: <METHOD>
```

The values ```GET``` and ```POST``` are valid for the ```method``` field.
The ```url``` field is the URL which you want to POST or GET.

Make sure to give your EventSource a ```name``` - this is later put in your app code in order to receive events from this source.

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./http.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy http.yaml to that directory.

```
mkdir -p eventsources
cp http.yaml ./eventsources
```