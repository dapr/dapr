// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clientv3

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	etcdclient "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/credentials"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/third_party/etcd/client/internal/endpoint"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/third_party/etcd/client/internal/resolver"
)

var (
	ErrNoAvailableEndpoints = errors.New("etcdclient: no available endpoints")
	ErrOldCluster           = errors.New("etcdclient: old cluster version")
)

// Client provides and manages an etcd v3 client session.
type Client struct {
	etcdclient.Cluster
	etcdclient.KV
	etcdclient.Lease
	etcdclient.Watcher
	etcdclient.Auth
	etcdclient.Maintenance

	conn *grpc.ClientConn

	cfg      etcdclient.Config
	creds    grpccredentials.TransportCredentials
	resolver *resolver.EtcdManualResolver
	mu       *sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	// Username is a user name for authentication.
	Username string
	// Password is a password for authentication.
	Password        string
	authTokenBundle credentials.Bundle

	callOpts []grpc.CallOption

	lgMu *sync.RWMutex
	lg   *zap.Logger
}

// New creates a new etcdv3 client from a given configuration.
// func New(cfg etcdclient.Config) (*Client, error) {
func New(cfg etcdclient.Config) (*etcdclient.Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, ErrNoAvailableEndpoints
	}

	return newClient(&cfg)
}

// Option is a function type that can be passed as argument to NewCtxClient to configure client
type Option func(*Client)

// WithLogger overrides the logger.
//
// Deprecated: Please use WithZapLogger or Logger field in clientv3.Config
//
// Does not changes grpcLogger, that can be explicitly configured
// using grpc_zap.ReplaceGrpcLoggerV2(..) method.
func (c *Client) WithLogger(lg *zap.Logger) *Client {
	c.lgMu.Lock()
	c.lg = lg
	c.lgMu.Unlock()
	return c
}

// GetLogger gets the logger.
// NOTE: This method is for internal use of etcd-client library and should not be used as general-purpose logger.
func (c *Client) GetLogger() *zap.Logger {
	c.lgMu.RLock()
	l := c.lg
	c.lgMu.RUnlock()
	return l
}

// Close shuts down the client's etcd connections.
func (c *Client) Close() error {
	c.cancel()
	if c.Watcher != nil {
		c.Watcher.Close()
	}
	if c.Lease != nil {
		c.Lease.Close()
	}
	if c.conn != nil {
		return ContextError(c.ctx, c.conn.Close())
	}
	return c.ctx.Err()
}

// Ctx is a context for "out of band" messages (e.g., for sending
// "clean up" message when another context is canceled). It is
// canceled on client Close().
func (c *Client) Ctx() context.Context { return c.ctx }

// Endpoints lists the registered endpoints for the client.
func (c *Client) Endpoints() []string {
	// copy the slice; protect original endpoints from being changed
	c.mu.RLock()
	defer c.mu.RUnlock()
	eps := make([]string, len(c.cfg.Endpoints))
	copy(eps, c.cfg.Endpoints)
	return eps
}

// SetEndpoints updates client's endpoints.
func (c *Client) SetEndpoints(eps ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Endpoints = eps

	c.resolver.SetEndpoints(eps)
}

// Sync synchronizes client's endpoints with the known endpoints from the etcd membership.
func (c *Client) Sync(ctx context.Context) error {
	mresp, err := c.MemberList(ctx)
	if err != nil {
		return err
	}
	var eps []string
	for _, m := range mresp.Members {
		if len(m.Name) != 0 && !m.IsLearner {
			eps = append(eps, m.ClientURLs...)
		}
	}
	c.SetEndpoints(eps...)
	return nil
}

func (c *MyClient) autoSync() {
	if c.cfg.AutoSyncInterval == time.Duration(0) {
		return
	}

	for {
		select {
		case <-c.internalClient.Ctx().Done():
			return
		case <-time.After(c.cfg.AutoSyncInterval):
			ctx, cancel := context.WithTimeout(c.internalClient.Ctx(), 5*time.Second)
			err := c.internalClient.Sync(ctx)
			cancel()
			if err != nil && err != c.internalClient.Ctx().Err() {
				c.internalClient.GetLogger().Info("Auto sync endpoints failed.", zap.Error(err))
			}
		}
	}
}

// dialSetupOpts gives the dial opts prior to any authentication.
// func (c *Client) dialSetupOpts(creds grpccredentials.TransportCredentials, dopts ...grpc.DialOption) (opts []grpc.DialOption, err error) {
func (c *MyClient) dialSetupOpts(dopts ...grpc.DialOption) (opts []grpc.DialOption, err error) {
	if c.cfg.DialKeepAliveTime > 0 {
		params := keepalive.ClientParameters{
			Time:                c.cfg.DialKeepAliveTime,
			Timeout:             c.cfg.DialKeepAliveTimeout,
			PermitWithoutStream: c.cfg.PermitWithoutStream,
		}
		opts = append(opts, grpc.WithKeepaliveParams(params))
	}
	opts = append(opts, dopts...)

	unaryMaxRetries := defaultUnaryMaxRetries
	if c.cfg.MaxUnaryRetries > 0 {
		unaryMaxRetries = c.cfg.MaxUnaryRetries
	}

	backoffWaitBetween := defaultBackoffWaitBetween
	if c.cfg.BackoffWaitBetween > 0 {
		backoffWaitBetween = c.cfg.BackoffWaitBetween
	}

	backoffJitterFraction := defaultBackoffJitterFraction
	if c.cfg.BackoffJitterFraction > 0 {
		backoffJitterFraction = c.cfg.BackoffJitterFraction
	}

	// Interceptor retry and backoff.
	// TODO: Replace all of clientv3/retry.go with RetryPolicy:
	// https://github.com/grpc/grpc-proto/blob/cdd9ed5c3d3f87aef62f373b93361cf7bddc620d/grpc/service_config/service_config.proto#L130
	rrBackoff := withBackoff(c.roundRobinQuorumBackoff(backoffWaitBetween, backoffJitterFraction))
	opts = append(opts,
		// Disable stream retry by default since go-grpc-middleware/retry does not support client streams.
		// Streams that are safe to retry are enabled individually.
		grpc.WithStreamInterceptor(c.streamClientInterceptor(withMax(0), rrBackoff)),
		grpc.WithUnaryInterceptor(c.unaryClientInterceptor(withMax(unaryMaxRetries), rrBackoff)),
	)

	return opts, nil
}

// Dial connects to a single endpoint using the client's config.
func (c *MyClient) Dial(ep string) (*grpc.ClientConn, error) {
	// Using ad-hoc created resolver, to guarantee only explicitly given
	// endpoint is used.
	return c.dial(grpc.WithResolvers(resolver.New(ep)))
}

func (c *Client) getToken(ctx context.Context) error {
	var err error // return last error in a case of fail

	if c.Username == "" || c.Password == "" {
		return nil
	}

	resp, err := c.Auth.Authenticate(ctx, c.Username, c.Password)
	if err != nil {
		if err == rpctypes.ErrAuthNotEnabled {
			c.authTokenBundle.UpdateAuthToken("")
			return nil
		}
		return err
	}
	c.authTokenBundle.UpdateAuthToken(resp.Token)
	return nil
}

// dialWithBalancer dials the client's current load balanced resolver group.  The scheme of the host
// of the provided endpoint determines the scheme used for all endpoints of the client connection.
// func (c *Client) dialWithBalancer(dopts ...grpc.DialOption) (*grpc.ClientConn, error) {
func (c *MyClient) dialWithBalancer(dopts ...grpc.DialOption) (*grpc.ClientConn, error) {
	//creds := c.credentialsForEndpoint(c.Endpoints()[0])
	opts := append(dopts, grpc.WithResolvers(c.resolver))
	return c.dial(opts...)
}

// dial configures and dials any grpc balancer target.
// func (c *Client) dial(creds grpccredentials.TransportCredentials, dopts ...grpc.DialOption) (*grpc.ClientConn, error) {
func (c *MyClient) dial(dopts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts, err := c.dialSetupOpts(dopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to configure dialer: %v", err)
	}

	opts = append(opts, c.cfg.DialOptions...)

	dctx := c.internalClient.Ctx()
	if c.cfg.DialTimeout > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(c.cfg.Context, c.cfg.DialTimeout)
		defer cancel() // TODO: Is this right for cases where grpc.WithBlock() is not set on the dial options?
	}
	target := fmt.Sprintf("%s://%p/%s", resolver.Schema, c, authority(c.internalClient.Endpoints()[0]))
	// cassie todo: here need to ensure we are using the security pkg NetDialerID

	conn, err := grpc.DialContext(dctx, target, opts...)

	if err != nil {
		return nil, err
	}
	return conn, nil
}

func authority(endpoint string) string {
	spl := strings.SplitN(endpoint, "://", 2)
	if len(spl) < 2 {
		if strings.HasPrefix(endpoint, "unix:") {
			return endpoint[len("unix:"):]
		}
		if strings.HasPrefix(endpoint, "unixs:") {
			return endpoint[len("unixs:"):]
		}
		return endpoint
	}
	return spl[1]
}

func (c *Client) credentialsForEndpoint(ep string) grpccredentials.TransportCredentials {
	r := endpoint.RequiresCredentials(ep)
	switch r {
	case endpoint.CREDS_DROP:
		return nil
	case endpoint.CREDS_OPTIONAL:
		return c.creds
	case endpoint.CREDS_REQUIRE:
		if c.creds != nil {
			return c.creds
		}
		return credentials.NewBundle(credentials.Config{}).TransportCredentials()
	default:
		panic(fmt.Errorf("unsupported CredsRequirement: %v", r))
	}
}

type MyClient struct {
	internalClient *etcdclient.Client
	conn           grpc.ClientConnInterface
	resolver       *resolver.EtcdManualResolver
	cfg            *etcdclient.Config
	callOpts       []grpc.CallOption
}

func newClient(cfg *etcdclient.Config) (*etcdclient.Client, error) {
	if cfg == nil {
		cfg = &etcdclient.Config{}
	}

	// use a temporary skeleton client to bootstrap first connection
	baseCtx := context.TODO()
	if cfg.Context != nil {
		baseCtx = cfg.Context
	}

	ctx, cancel := context.WithCancel(baseCtx)

	myClient := &MyClient{
		resolver: resolver.New(cfg.Endpoints...),
		cfg:      cfg,
	}

	etcdclientCfg := &etcdclient.Config{
		Endpoints:             cfg.Endpoints,
		AutoSyncInterval:      cfg.AutoSyncInterval,
		DialTimeout:           cfg.DialTimeout,
		DialKeepAliveTime:     cfg.DialKeepAliveTime,
		DialKeepAliveTimeout:  cfg.DialKeepAliveTimeout,
		MaxCallSendMsgSize:    cfg.MaxCallSendMsgSize,
		MaxCallRecvMsgSize:    cfg.MaxCallRecvMsgSize,
		TLS:                   cfg.TLS,
		Username:              cfg.Username,
		Password:              cfg.Password,
		RejectOldCluster:      cfg.RejectOldCluster,
		DialOptions:           cfg.DialOptions,
		Context:               cfg.Context,
		Logger:                cfg.Logger,
		LogConfig:             cfg.LogConfig,
		PermitWithoutStream:   cfg.PermitWithoutStream,
		MaxUnaryRetries:       cfg.MaxUnaryRetries,
		BackoffWaitBetween:    cfg.BackoffWaitBetween,
		BackoffJitterFraction: cfg.BackoffJitterFraction,
	}
	internalClient, err := etcdclient.New(*etcdclientCfg)
	if err != nil {
		return nil, err
	}
	myClient.internalClient = internalClient

	//var err error
	//if cfg.Logger != nil {
	//	client.lg = cfg.Logger
	//} else if cfg.LogConfig != nil {
	//	client.lg, err = cfg.LogConfig.Build()
	//} else {
	//	client.lg, err = logutil.CreateDefaultZapLogger(etcdClientDebugLevel())
	//	if client.lg != nil {
	//		client.lg = client.lg.Named("etcd-client")
	//	}
	//}
	//if err != nil {
	//	return nil, err
	//}

	if cfg.MaxCallSendMsgSize > 0 || cfg.MaxCallRecvMsgSize > 0 {
		if cfg.MaxCallRecvMsgSize > 0 && cfg.MaxCallSendMsgSize > cfg.MaxCallRecvMsgSize {
			return nil, fmt.Errorf("gRPC message recv limit (%d bytes) must be greater than send limit (%d bytes)", cfg.MaxCallRecvMsgSize, cfg.MaxCallSendMsgSize)
		}
		callOpts := []grpc.CallOption{
			defaultWaitForReady,
			defaultMaxCallSendMsgSize,
			defaultMaxCallRecvMsgSize,
		}
		if cfg.MaxCallSendMsgSize > 0 {
			callOpts[1] = grpc.MaxCallSendMsgSize(cfg.MaxCallSendMsgSize)
		}
		if cfg.MaxCallRecvMsgSize > 0 {
			callOpts[2] = grpc.MaxCallRecvMsgSize(cfg.MaxCallRecvMsgSize)
		}
		myClient.callOpts = callOpts
	}

	if len(cfg.Endpoints) < 1 {
		cancel()
		return nil, fmt.Errorf("at least one Endpoint is required in client config")
	}

	// Use a provided endpoint target so that for https:// without any tls config given, then
	// grpc will assume the certificate server name is the endpoint host.
	conn, err := myClient.dialWithBalancer(cfg.DialOptions...)

	myClient.conn = conn
	myClient.internalClient.Cluster = etcdclient.NewCluster(myClient.internalClient)
	myClient.internalClient.KV = etcdclient.NewKV(myClient.internalClient)
	myClient.internalClient.Lease = etcdclient.NewLease(myClient.internalClient)
	myClient.internalClient.Watcher = etcdclient.NewWatcher(myClient.internalClient)
	myClient.internalClient.Auth = etcdclient.NewAuth(myClient.internalClient)
	myClient.internalClient.Maintenance = etcdclient.NewMaintenance(myClient.internalClient)

	//get token with established connection
	ctx, cancel = myClient.internalClient.Ctx(), func() {}
	if myClient.cfg.DialTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, myClient.cfg.DialTimeout)
	}
	//err = client.getToken(ctx)
	//if err != nil {
	//	client.Close()
	//	cancel()
	//	//TODO: Consider fmt.Errorf("communicating with [%s] failed: %v", strings.Join(cfg.Endpoints, ";"), err)
	//	return nil, err
	//}
	cancel()

	//if cfg.RejectOldCluster {
	//	if err := client.checkVersion(); err != nil {
	//		client.Close()
	//		return nil, err
	//	}
	//}

	go myClient.autoSync()
	return myClient.internalClient, nil
}

// roundRobinQuorumBackoff retries against quorum between each backoff.
// This is intended for use with a round robin load balancer.
func (c *MyClient) roundRobinQuorumBackoff(waitBetween time.Duration, jitterFraction float64) backoffFunc {
	return func(attempt uint) time.Duration {
		// after each round robin across quorum, backoff for our wait between duration
		n := uint(len(c.internalClient.Endpoints()))
		quorum := (n/2 + 1)
		if attempt%quorum == 0 {

			c.internalClient.GetLogger().Debug("backoff", zap.Uint("attempt", attempt), zap.Uint("quorum", quorum), zap.Duration("waitBetween", waitBetween), zap.Float64("jitterFraction", jitterFraction))
			return jitterUp(waitBetween, jitterFraction)
		}
		c.internalClient.GetLogger().Debug("backoff skipped", zap.Uint("attempt", attempt), zap.Uint("quorum", quorum))
		return 0
	}
}

func (c *Client) checkVersion() (err error) {
	var wg sync.WaitGroup

	eps := c.Endpoints()
	errc := make(chan error, len(eps))
	ctx, cancel := context.WithCancel(c.ctx)
	if c.cfg.DialTimeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(c.ctx, c.cfg.DialTimeout)
	}

	wg.Add(len(eps))
	for _, ep := range eps {
		// if cluster is current, any endpoint gives a recent version
		go func(e string) {
			defer wg.Done()
			resp, rerr := c.Status(ctx, e)
			if rerr != nil {
				errc <- rerr
				return
			}
			vs := strings.Split(resp.Version, ".")
			maj, min := 0, 0
			if len(vs) >= 2 {
				var serr error
				if maj, serr = strconv.Atoi(vs[0]); serr != nil {
					errc <- serr
					return
				}
				if min, serr = strconv.Atoi(vs[1]); serr != nil {
					errc <- serr
					return
				}
			}
			if maj < 3 || (maj == 3 && min < 4) {
				rerr = ErrOldCluster
			}
			errc <- rerr
		}(ep)
	}
	// wait for success
	for range eps {
		if err = <-errc; err != nil {
			break
		}
	}
	cancel()
	wg.Wait()
	return err
}

// ActiveConnection returns the current in-use connection
func (c *Client) ActiveConnection() *grpc.ClientConn { return c.conn }

// ContextError converts the error into an EtcdError if the error message matches one of
// the defined messages; otherwise, it tries to retrieve the context error.
func ContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	err = rpctypes.Error(err)
	if _, ok := err.(rpctypes.EtcdError); ok {
		return err
	}
	if ev, ok := status.FromError(err); ok {
		code := ev.Code()
		switch code {
		case codes.DeadlineExceeded:
			fallthrough
		case codes.Canceled:
			if ctx.Err() != nil {
				err = ctx.Err()
			}
		}
	}
	return err
}
