# Kubernetes Config type Definition and Deepcopy Utility

This directory contains type definition and deepcopy utility copied from
k8s.io/client-go that are used for parsing and persist kube config yaml
file.

### types.go

The Config type definition is copied from k8s.io/client-go/tools/clientcmd/api/v1/types.go
for parsing the kube config yaml. The "k8s.io/apimachinery/pkg/runtime" dependency has
been removed. An example of using this type definition to parse a kube config
yaml is:

```go
	// Init an empty api.Config as unmarshal layout template
	c := api.Config{}
	err = yaml.Unmarshal(kubeConfig, &c)
	if err != nil {
		return nil, err
	}
```

### zz\_generated.deepcopy.go
The Config type deepcopy util file is copied from
k8s.io/client-go/tools/clientcmd/api/v1/zz\_generated.deepcopy.go
for deepcopy the kube config. The "k8s.io/apimachinery/pkg/runtime" dependency has
been removed.
