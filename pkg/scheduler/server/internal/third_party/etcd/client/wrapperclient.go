package clientv3

import etcdclient "go.etcd.io/etcd/client/v3"

// Wrapper provides a simplified interface around the Client.
type Wrapper struct {
	client *etcdclient.Client
}
