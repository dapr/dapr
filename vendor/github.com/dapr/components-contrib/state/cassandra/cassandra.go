// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cassandra

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dapr/components-contrib/state"
	"github.com/gocql/gocql"
	jsoniter "github.com/json-iterator/go"
)

const (
	hosts                    = "hosts"
	port                     = "port"
	username                 = "username"
	password                 = "password"
	protoVersion             = "protoVersion"
	consistency              = "consistency"
	table                    = "table"
	keyspace                 = "keyspace"
	replicationFactor        = "replicationFactor"
	defaultProtoVersion      = 4
	defaultReplicationFactor = 1
	defaultConsistency       = gocql.All
	defaultTable             = "items"
	defaultKeyspace          = "dapr"
	defaultPort              = 9042
)

// Cassandra is a state store implementation for Apache Cassandra
type Cassandra struct {
	session *gocql.Session
	cluster *gocql.ClusterConfig
	table   string
}

type cassandraMetadata struct {
	hosts             []string
	port              int
	protoVersion      int
	replicationFactor int
	username          string
	password          string
	consistency       string
	table             string
	keyspace          string
}

// NewCassandraStateStore returns a new cassandra state store
func NewCassandraStateStore() *Cassandra {
	return &Cassandra{}
}

// Init performs metadata and connection parsing
func (c *Cassandra) Init(metadata state.Metadata) error {
	meta, err := getCassandraMetadata(metadata)
	if err != nil {
		return err
	}

	cluster, err := c.createClusterConfig(meta)
	if err != nil {
		return fmt.Errorf("error creating cluster config: %s", err)
	}
	c.cluster = cluster

	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("error creating session: %s", err)
	}
	c.session = session

	err = c.tryCreateKeyspace(meta.keyspace, meta.replicationFactor)
	if err != nil {
		return fmt.Errorf("error creating keyspace %s: %s", meta.table, err)
	}

	err = c.tryCreateTable(meta.table, meta.keyspace)
	if err != nil {
		return fmt.Errorf("error creating keyspace %s: %s", meta.table, err)
	}

	c.table = fmt.Sprintf("%s.%s", meta.keyspace, meta.table)
	return nil
}

func (c *Cassandra) tryCreateKeyspace(keyspace string, replicationFactor int) error {
	return c.session.Query(fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH REPLICATION = {'class' : 'SimpleStrategy', 'replication_factor' : %s};", keyspace, fmt.Sprintf("%v", replicationFactor))).Exec()
}

func (c *Cassandra) tryCreateTable(table, keyspace string) error {
	return c.session.Query(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (key text, value blob, PRIMARY KEY (key));", keyspace, table)).Exec()
}

func (c *Cassandra) createClusterConfig(metadata *cassandraMetadata) (*gocql.ClusterConfig, error) {
	clusterConfig := gocql.NewCluster(metadata.hosts...)
	if metadata.username != "" && metadata.password != "" {
		clusterConfig.Authenticator = gocql.PasswordAuthenticator{Username: metadata.username, Password: metadata.password}
	}
	clusterConfig.Port = metadata.port
	clusterConfig.ProtoVersion = metadata.protoVersion
	cons, err := c.getConsistency(metadata.consistency)
	if err != nil {
		return nil, err
	}

	clusterConfig.Consistency = cons
	return clusterConfig, nil
}

func (c *Cassandra) getConsistency(consistency string) (gocql.Consistency, error) {
	switch consistency {
	case "All":
		return gocql.All, nil
	case "One":
		return gocql.One, nil
	case "Two":
		return gocql.Two, nil
	case "Three":
		return gocql.Three, nil
	case "Quorum":
		return gocql.Quorum, nil
	case "LocalQuorum":
		return gocql.LocalQuorum, nil
	case "EachQuorum":
		return gocql.EachQuorum, nil
	case "LocalOne":
		return gocql.LocalOne, nil
	case "Any":
		return gocql.Any, nil
	case "":
		return defaultConsistency, nil
	}
	return 0, fmt.Errorf("consistency mode %s not found", consistency)
}

func getCassandraMetadata(metadata state.Metadata) (*cassandraMetadata, error) {
	meta := cassandraMetadata{
		protoVersion:      defaultProtoVersion,
		table:             defaultTable,
		keyspace:          defaultKeyspace,
		replicationFactor: defaultReplicationFactor,
		consistency:       "All",
		port:              defaultPort,
	}

	if val, ok := metadata.Properties[hosts]; ok && val != "" {
		meta.hosts = strings.Split(val, ",")
	} else {
		return nil, errors.New("missing or empty hosts field from metadata")
	}

	if val, ok := metadata.Properties[port]; ok && val != "" {
		p, err := strconv.ParseInt(val, 0, 32)
		if err != nil {
			return nil, fmt.Errorf("error parsing port field: %s", err)
		}
		meta.port = int(p)
	}

	if val, ok := metadata.Properties[consistency]; ok && val != "" {
		meta.consistency = val
	}

	if val, ok := metadata.Properties[table]; ok && val != "" {
		meta.table = val
	}

	if val, ok := metadata.Properties[keyspace]; ok && val != "" {
		meta.keyspace = val
	}

	if val, ok := metadata.Properties[username]; ok && val != "" {
		meta.username = val
	}

	if val, ok := metadata.Properties[password]; ok && val != "" {
		meta.password = val
	}

	if val, ok := metadata.Properties[protoVersion]; ok && val != "" {
		p, err := strconv.ParseInt(val, 0, 32)
		if err != nil {
			return nil, fmt.Errorf("error parsing protoVersion field: %s", err)
		}
		meta.protoVersion = int(p)
	}

	if val, ok := metadata.Properties[replicationFactor]; ok && val != "" {
		r, err := strconv.ParseInt(val, 0, 32)
		if err != nil {
			return nil, fmt.Errorf("error parsing replicationFactor field: %s", err)
		}
		meta.replicationFactor = int(r)
	}

	return &meta, nil
}

// Delete performs a delete operation
func (c *Cassandra) Delete(req *state.DeleteRequest) error {
	return c.session.Query("DELETE FROM ? WHERE key = ?", c.table, req.Key).Exec()
}

// BulkDelete performs a bulk delete operation
func (c *Cassandra) BulkDelete(req []state.DeleteRequest) error {
	for _, re := range req {
		err := c.Delete(&re)
		if err != nil {
			return err
		}
	}

	return nil
}

// Get retrieves state from cassandra with a key
func (c *Cassandra) Get(req *state.GetRequest) (*state.GetResponse, error) {
	session := c.session

	if req.Options.Consistency == state.Strong {
		sess, err := c.createSession(gocql.All)
		if err != nil {
			return nil, err
		}
		defer sess.Close()
		session = sess
	} else if req.Options.Consistency == state.Eventual {
		sess, err := c.createSession(gocql.One)
		if err != nil {
			return nil, err
		}
		defer sess.Close()
		session = sess
	}

	results, err := session.Query("SELECT value FROM ? WHERE key = ?", c.table, req.Key).Iter().SliceMap()
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return &state.GetResponse{}, nil
	}
	return &state.GetResponse{
		Data: results[0]["value"].([]byte),
	}, nil
}

// Set saves state into cassandra
func (c *Cassandra) Set(req *state.SetRequest) error {
	var bt []byte
	b, ok := req.Value.([]byte)
	if ok {
		bt = b
	} else {
		bt, _ = jsoniter.ConfigFastest.Marshal(req.Value)
	}

	session := c.session

	if req.Options.Consistency == state.Strong {
		sess, err := c.createSession(gocql.Quorum)
		if err != nil {
			return err
		}
		defer sess.Close()
		session = sess
	} else if req.Options.Consistency == state.Eventual {
		sess, err := c.createSession(gocql.Any)
		if err != nil {
			return err
		}
		defer sess.Close()
		session = sess
	}

	return session.Query("INSERT INTO ? (key, value) VALUES (?, ?)", c.table, req.Key, bt).Exec()
}

func (c *Cassandra) createSession(consistency gocql.Consistency) (*gocql.Session, error) {
	session, err := c.cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("error creating session: %s", err)
	}

	session.SetConsistency(consistency)
	return session, nil
}

// BulkSet performs a bulks save operation
func (c *Cassandra) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}
	return nil
}
