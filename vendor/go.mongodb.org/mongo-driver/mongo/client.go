// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongo

import (
	"context"
	"crypto/tls"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/auth"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/operation"
	"go.mongodb.org/mongo-driver/x/mongo/driver/session"
	"go.mongodb.org/mongo-driver/x/mongo/driver/topology"
	"go.mongodb.org/mongo-driver/x/mongo/driver/uuid"
)

const defaultLocalThreshold = 15 * time.Millisecond
const batchSize = 10000

// Client performs operations on a given topology.
type Client struct {
	id              uuid.UUID
	topologyOptions []topology.Option
	topology        *topology.Topology
	connString      connstring.ConnString
	localThreshold  time.Duration
	retryWrites     bool
	retryReads      bool
	clock           *session.ClusterClock
	readPreference  *readpref.ReadPref
	readConcern     *readconcern.ReadConcern
	writeConcern    *writeconcern.WriteConcern
	registry        *bsoncodec.Registry
	marshaller      BSONAppender
	monitor         *event.CommandMonitor
}

// Connect creates a new Client and then initializes it using the Connect method.
func Connect(ctx context.Context, opts ...*options.ClientOptions) (*Client, error) {
	c, err := NewClient(opts...)
	if err != nil {
		return nil, err
	}
	err = c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// NewClient creates a new client to connect to a cluster specified by the uri.
//
// When creating an options.ClientOptions, the order the methods are called matters. Later Set*
// methods will overwrite the values from previous Set* method invocations. This includes the
// ApplyURI method. This allows callers to determine the order of precedence for option
// application. For instance, if ApplyURI is called before SetAuth, the Credential from
// SetAuth will overwrite the values from the connection string. If ApplyURI is called
// after SetAuth, then its values will overwrite those from SetAuth.
//
// The opts parameter is processed using options.MergeClientOptions, which will overwrite entire
// option fields of previous options, there is no partial overwriting. For example, if Username is
// set in the Auth field for the first option, and Password is set for the second but with no
// Username, after the merge the Username field will be empty.
func NewClient(opts ...*options.ClientOptions) (*Client, error) {
	clientOpt := options.MergeClientOptions(opts...)

	id, err := uuid.New()
	if err != nil {
		return nil, err
	}
	client := &Client{id: id}

	err = client.configure(clientOpt)
	if err != nil {
		return nil, err
	}

	client.topology, err = topology.New(client.topologyOptions...)
	if err != nil {
		return nil, replaceErrors(err)
	}

	return client, nil
}

// Connect initializes the Client by starting background monitoring goroutines.
// This method must be called before a Client can be used.
func (c *Client) Connect(ctx context.Context) error {
	err := c.topology.Connect()
	if err != nil {
		return replaceErrors(err)
	}

	return nil

}

// Disconnect closes sockets to the topology referenced by this Client. It will
// shut down any monitoring goroutines, close the idle connection pool, and will
// wait until all the in use connections have been returned to the connection
// pool and closed before returning. If the context expires via cancellation,
// deadline, or timeout before the in use connections have returned, the in use
// connections will be closed, resulting in the failure of any in flight read
// or write operations. If this method returns with no errors, all connections
// associated with this Client have been closed.
func (c *Client) Disconnect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.endSessions(ctx)
	return replaceErrors(c.topology.Disconnect(ctx))
}

// Ping verifies that the client can connect to the topology.
// If readPreference is nil then will use the client's default read
// preference.
func (c *Client) Ping(ctx context.Context, rp *readpref.ReadPref) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if rp == nil {
		rp = c.readPreference
	}

	db := c.Database("admin")
	res := db.RunCommand(ctx, bson.D{
		{"ping", 1},
	}, options.RunCmd().SetReadPreference(rp))

	return replaceErrors(res.Err())
}

// StartSession starts a new session.
func (c *Client) StartSession(opts ...*options.SessionOptions) (Session, error) {
	if c.topology.SessionPool == nil {
		return nil, ErrClientDisconnected
	}

	sopts := options.MergeSessionOptions(opts...)
	coreOpts := &session.ClientOptions{
		DefaultReadConcern:    c.readConcern,
		DefaultReadPreference: c.readPreference,
		DefaultWriteConcern:   c.writeConcern,
	}
	if sopts.CausalConsistency != nil {
		coreOpts.CausalConsistency = sopts.CausalConsistency
	}
	if sopts.DefaultReadConcern != nil {
		coreOpts.DefaultReadConcern = sopts.DefaultReadConcern
	}
	if sopts.DefaultWriteConcern != nil {
		coreOpts.DefaultWriteConcern = sopts.DefaultWriteConcern
	}
	if sopts.DefaultReadPreference != nil {
		coreOpts.DefaultReadPreference = sopts.DefaultReadPreference
	}
	if sopts.DefaultMaxCommitTime != nil {
		coreOpts.DefaultMaxCommitTime = sopts.DefaultMaxCommitTime
	}

	sess, err := session.NewClientSession(c.topology.SessionPool, c.id, session.Explicit, coreOpts)
	if err != nil {
		return nil, replaceErrors(err)
	}

	sess.RetryWrite = c.retryWrites
	sess.RetryRead = c.retryReads

	return &sessionImpl{
		clientSession: sess,
		client:        c,
		topo:          c.topology,
	}, nil
}

func (c *Client) endSessions(ctx context.Context) {
	if c.topology.SessionPool == nil {
		return
	}

	ids := c.topology.SessionPool.IDSlice()
	idx, idArray := bsoncore.AppendArrayStart(nil)
	for i, id := range ids {
		idDoc, _ := id.MarshalBSON()
		idArray = bsoncore.AppendDocumentElement(idArray, strconv.Itoa(i), idDoc)
	}
	idArray, _ = bsoncore.AppendArrayEnd(idArray, idx)

	op := operation.NewEndSessions(idArray).ClusterClock(c.clock).Deployment(c.topology).
		ServerSelector(description.ReadPrefSelector(readpref.PrimaryPreferred())).CommandMonitor(c.monitor).Database("admin")

	idx, idArray = bsoncore.AppendArrayStart(nil)
	totalNumIDs := len(ids)
	for i := 0; i < totalNumIDs; i++ {
		idDoc, _ := ids[i].MarshalBSON()
		idArray = bsoncore.AppendDocumentElement(idArray, strconv.Itoa(i), idDoc)
		if ((i+1)%batchSize) == 0 || i == totalNumIDs-1 {
			idArray, _ = bsoncore.AppendArrayEnd(idArray, idx)
			_ = op.SessionIDs(idArray).Execute(ctx)
			idArray = idArray[:0]
			idx = 0
		}
	}

}

func (c *Client) configure(opts *options.ClientOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}

	var connOpts []topology.ConnectionOption
	var serverOpts []topology.ServerOption
	var topologyOpts []topology.Option

	// TODO(GODRIVER-814): Add tests for topology, server, and connection related options.

	// AppName
	var appName string
	if opts.AppName != nil {
		appName = *opts.AppName
	}
	// Compressors & ZlibLevel
	var comps []string
	if len(opts.Compressors) > 0 {
		comps = opts.Compressors

		connOpts = append(connOpts, topology.WithCompressors(
			func(compressors []string) []string {
				return append(compressors, comps...)
			},
		))

		for _, comp := range comps {
			if comp == "zlib" {
				connOpts = append(connOpts, topology.WithZlibLevel(func(level *int) *int {
					return opts.ZlibLevel
				}))
			}
		}

		serverOpts = append(serverOpts, topology.WithCompressionOptions(
			func(opts ...string) []string { return append(opts, comps...) },
		))
	}
	// Handshaker
	var handshaker = func(driver.Handshaker) driver.Handshaker {
		return operation.NewIsMaster().AppName(appName).Compressors(comps)
	}
	// Auth & Database & Password & Username
	if opts.Auth != nil {
		cred := &auth.Cred{
			Username:    opts.Auth.Username,
			Password:    opts.Auth.Password,
			PasswordSet: opts.Auth.PasswordSet,
			Props:       opts.Auth.AuthMechanismProperties,
			Source:      opts.Auth.AuthSource,
		}
		mechanism := opts.Auth.AuthMechanism

		if len(cred.Source) == 0 {
			switch strings.ToUpper(mechanism) {
			case auth.MongoDBX509, auth.GSSAPI, auth.PLAIN:
				cred.Source = "$external"
			default:
				cred.Source = "admin"
			}
		}

		authenticator, err := auth.CreateAuthenticator(mechanism, cred)
		if err != nil {
			return err
		}

		handshakeOpts := &auth.HandshakeOptions{
			AppName:       appName,
			Authenticator: authenticator,
			Compressors:   comps,
		}
		if mechanism == "" {
			// Required for SASL mechanism negotiation during handshake
			handshakeOpts.DBUser = cred.Source + "." + cred.Username
		}
		if opts.AuthenticateToAnything != nil && *opts.AuthenticateToAnything {
			// Authenticate arbiters
			handshakeOpts.PerformAuthentication = func(serv description.Server) bool {
				return true
			}
		}

		handshaker = func(driver.Handshaker) driver.Handshaker {
			return auth.Handshaker(nil, handshakeOpts)
		}
	}
	connOpts = append(connOpts, topology.WithHandshaker(handshaker))
	// ConnectTimeout
	if opts.ConnectTimeout != nil {
		serverOpts = append(serverOpts, topology.WithHeartbeatTimeout(
			func(time.Duration) time.Duration { return *opts.ConnectTimeout },
		))
		connOpts = append(connOpts, topology.WithConnectTimeout(
			func(time.Duration) time.Duration { return *opts.ConnectTimeout },
		))
	}
	// Dialer
	if opts.Dialer != nil {
		connOpts = append(connOpts, topology.WithDialer(
			func(topology.Dialer) topology.Dialer { return opts.Dialer },
		))
	}
	// Direct
	if opts.Direct != nil && *opts.Direct {
		topologyOpts = append(topologyOpts, topology.WithMode(
			func(topology.MonitorMode) topology.MonitorMode { return topology.SingleMode },
		))
	}
	// HeartbeatInterval
	if opts.HeartbeatInterval != nil {
		serverOpts = append(serverOpts, topology.WithHeartbeatInterval(
			func(time.Duration) time.Duration { return *opts.HeartbeatInterval },
		))
	}
	// Hosts
	hosts := []string{"localhost:27017"} // default host
	if len(opts.Hosts) > 0 {
		hosts = opts.Hosts
	}
	topologyOpts = append(topologyOpts, topology.WithSeedList(
		func(...string) []string { return hosts },
	))
	// LocalThreshold
	c.localThreshold = defaultLocalThreshold
	if opts.LocalThreshold != nil {
		c.localThreshold = *opts.LocalThreshold
	}
	// MaxConIdleTime
	if opts.MaxConnIdleTime != nil {
		connOpts = append(connOpts, topology.WithIdleTimeout(
			func(time.Duration) time.Duration { return *opts.MaxConnIdleTime },
		))
	}
	// MaxPoolSize
	if opts.MaxPoolSize != nil {
		serverOpts = append(
			serverOpts,
			topology.WithMaxConnections(func(uint64) uint64 { return *opts.MaxPoolSize }),
		)
	}
	// MinPoolSize
	if opts.MinPoolSize != nil {
		serverOpts = append(
			serverOpts,
			topology.WithMinConnections(func(uint64) uint64 { return *opts.MinPoolSize }),
		)
	}
	// PoolMonitor
	if opts.PoolMonitor != nil {
		serverOpts = append(
			serverOpts,
			topology.WithConnectionPoolMonitor(func(*event.PoolMonitor) *event.PoolMonitor { return opts.PoolMonitor }),
		)
	}
	// Monitor
	if opts.Monitor != nil {
		c.monitor = opts.Monitor
		connOpts = append(connOpts, topology.WithMonitor(
			func(*event.CommandMonitor) *event.CommandMonitor { return opts.Monitor },
		))
	}
	// ReadConcern
	c.readConcern = readconcern.New()
	if opts.ReadConcern != nil {
		c.readConcern = opts.ReadConcern
	}
	// ReadPreference
	c.readPreference = readpref.Primary()
	if opts.ReadPreference != nil {
		c.readPreference = opts.ReadPreference
	}
	// Registry
	c.registry = bson.DefaultRegistry
	if opts.Registry != nil {
		c.registry = opts.Registry
	}
	// ReplicaSet
	if opts.ReplicaSet != nil {
		topologyOpts = append(topologyOpts, topology.WithReplicaSetName(
			func(string) string { return *opts.ReplicaSet },
		))
	}
	// RetryWrites
	c.retryWrites = true // retry writes on by default
	if opts.RetryWrites != nil {
		c.retryWrites = *opts.RetryWrites
	}
	c.retryReads = true
	if opts.RetryReads != nil {
		c.retryReads = *opts.RetryReads
	}
	// ServerSelectionTimeout
	if opts.ServerSelectionTimeout != nil {
		topologyOpts = append(topologyOpts, topology.WithServerSelectionTimeout(
			func(time.Duration) time.Duration { return *opts.ServerSelectionTimeout },
		))
	}
	// SocketTimeout
	if opts.SocketTimeout != nil {
		connOpts = append(
			connOpts,
			topology.WithReadTimeout(func(time.Duration) time.Duration { return *opts.SocketTimeout }),
			topology.WithWriteTimeout(func(time.Duration) time.Duration { return *opts.SocketTimeout }),
		)
	}
	// TLSConfig
	if opts.TLSConfig != nil {
		connOpts = append(connOpts, topology.WithTLSConfig(
			func(*tls.Config) *tls.Config {
				return opts.TLSConfig
			},
		))
	}
	// WriteConcern
	if opts.WriteConcern != nil {
		c.writeConcern = opts.WriteConcern
	}

	// ClusterClock
	c.clock = new(session.ClusterClock)

	serverOpts = append(
		serverOpts,
		topology.WithClock(func(*session.ClusterClock) *session.ClusterClock { return c.clock }),
		topology.WithConnectionOptions(func(...topology.ConnectionOption) []topology.ConnectionOption { return connOpts }),
	)
	c.topologyOptions = append(topologyOpts, topology.WithServerOptions(
		func(...topology.ServerOption) []topology.ServerOption { return serverOpts },
	))

	return nil
}

// validSession returns an error if the session doesn't belong to the client
func (c *Client) validSession(sess *session.Client) error {
	if sess != nil && !uuid.Equal(sess.ClientID, c.id) {
		return ErrWrongClient
	}
	return nil
}

// Database returns a handle for a given database.
func (c *Client) Database(name string, opts ...*options.DatabaseOptions) *Database {
	return newDatabase(c, name, opts...)
}

// ListDatabases returns a ListDatabasesResult.
func (c *Client) ListDatabases(ctx context.Context, filter interface{}, opts ...*options.ListDatabasesOptions) (ListDatabasesResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sess := sessionFromContext(ctx)

	err := c.validSession(sess)
	if sess == nil && c.topology.SessionPool != nil {
		sess, err = session.NewClientSession(c.topology.SessionPool, c.id, session.Implicit)
		if err != nil {
			return ListDatabasesResult{}, err
		}
		defer sess.EndSession()
	}

	err = c.validSession(sess)
	if err != nil {
		return ListDatabasesResult{}, err
	}

	filterDoc, err := transformBsoncoreDocument(c.registry, filter)
	if err != nil {
		return ListDatabasesResult{}, err
	}

	selector := description.CompositeSelector([]description.ServerSelector{
		description.ReadPrefSelector(readpref.Primary()),
		description.LatencySelector(c.localThreshold),
	})
	selector = makeReadPrefSelector(sess, selector, c.localThreshold)

	ldo := options.MergeListDatabasesOptions(opts...)
	op := operation.NewListDatabases(filterDoc).
		Session(sess).ReadPreference(c.readPreference).CommandMonitor(c.monitor).
		ServerSelector(selector).ClusterClock(c.clock).Database("admin").Deployment(c.topology)
	if ldo.NameOnly != nil {
		op = op.NameOnly(*ldo.NameOnly)
	}
	retry := driver.RetryNone
	if c.retryReads {
		retry = driver.RetryOncePerCommand
	}
	op.Retry(retry)

	err = op.Execute(ctx)
	if err != nil {
		return ListDatabasesResult{}, replaceErrors(err)
	}

	return newListDatabasesResultFromOperation(op.Result()), nil
}

// ListDatabaseNames returns a slice containing the names of all of the databases on the server.
func (c *Client) ListDatabaseNames(ctx context.Context, filter interface{}, opts ...*options.ListDatabasesOptions) ([]string, error) {
	opts = append(opts, options.ListDatabases().SetNameOnly(true))

	res, err := c.ListDatabases(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0)
	for _, spec := range res.Databases {
		names = append(names, spec.Name)
	}

	return names, nil
}

// WithSession allows a user to start a session themselves and manage
// its lifetime. The only way to provide a session to a CRUD method is
// to invoke that CRUD method with the mongo.SessionContext within the
// closure. The mongo.SessionContext can be used as a regular context,
// so methods like context.WithDeadline and context.WithTimeout are
// supported.
//
// If the context.Context already has a mongo.Session attached, that
// mongo.Session will be replaced with the one provided.
//
// Errors returned from the closure are transparently returned from
// this function.
func WithSession(ctx context.Context, sess Session, fn func(SessionContext) error) error {
	return fn(contextWithSession(ctx, sess))
}

// UseSession creates a default session, that is only valid for the
// lifetime of the closure. No cleanup outside of closing the session
// is done upon exiting the closure. This means that an outstanding
// transaction will be aborted, even if the closure returns an error.
//
// If ctx already contains a mongo.Session, that mongo.Session will be
// replaced with the newly created mongo.Session.
//
// Errors returned from the closure are transparently returned from
// this method.
func (c *Client) UseSession(ctx context.Context, fn func(SessionContext) error) error {
	return c.UseSessionWithOptions(ctx, options.Session(), fn)
}

// UseSessionWithOptions works like UseSession but allows the caller
// to specify the options used to create the session.
func (c *Client) UseSessionWithOptions(ctx context.Context, opts *options.SessionOptions, fn func(SessionContext) error) error {
	defaultSess, err := c.StartSession(opts)
	if err != nil {
		return err
	}

	defer defaultSess.EndSession(ctx)

	sessCtx := sessionContext{
		Context: context.WithValue(ctx, sessionKey{}, defaultSess),
		Session: defaultSess,
	}

	return fn(sessCtx)
}

// Watch returns a change stream cursor used to receive information of changes to the client. This method is preferred
// to running a raw aggregation with a $changeStream stage because it supports resumability in the case of some errors.
// The client must have read concern majority or no read concern for a change stream to be created successfully.
func (c *Client) Watch(ctx context.Context, pipeline interface{},
	opts ...*options.ChangeStreamOptions) (*ChangeStream, error) {
	if c.topology.SessionPool == nil {
		return nil, ErrClientDisconnected
	}

	csConfig := changeStreamConfig{
		readConcern:    c.readConcern,
		readPreference: c.readPreference,
		client:         c,
		registry:       c.registry,
		streamType:     ClientStream,
	}

	return newChangeStream(ctx, csConfig, pipeline, opts...)
}
