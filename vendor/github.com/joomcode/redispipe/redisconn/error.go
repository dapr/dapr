package redisconn

import (
	"github.com/joomcode/errorx"
	"github.com/joomcode/redispipe/redis"
)

var (
	// ErrConnection - connection was not established at the moment request were done,
	// request is definitely not sent anywhere.
	ErrConnection = redis.Errors.NewSubNamespace("connection", redis.ErrTraitNotSent, redis.ErrTraitConnectivity)
	// ErrNotConnected - connection were not established at the moment
	ErrNotConnected = ErrConnection.NewType("not_connected")
	// ErrDial - could not connect.
	ErrDial = ErrConnection.NewType("could_not_connect")
	// ErrAuth - password didn't match
	ErrAuth = ErrConnection.NewType("count_not_auth", ErrTraitInitPermanent)
	// ErrInit - other error during initial conversation with redis
	ErrInit = ErrConnection.NewType("initialization_error", ErrTraitInitPermanent)
	// ErrConnSetup - other connection initialization error (including io errors)
	ErrConnSetup = ErrConnection.NewType("initialization_temp_error")

	// ErrTraitInitPermanent signals about non-transient error in initial communication with redis.
	// It means that either authentication fails or selected database doesn't exists or redis
	// behaves in unexpected way.
	ErrTraitInitPermanent = errorx.RegisterTrait("init_permanent")
)

var (
	// EKConnection - key for connection that handled request.
	EKConnection = errorx.RegisterProperty("connection")
	// EKDb - db number to select.
	EKDb = errorx.RegisterPrintableProperty("db")
)

func withNewProperty(err *errorx.Error, p errorx.Property, v interface{}) *errorx.Error {
	_, ok := err.Property(p)
	if ok {
		return err
	}
	return err.WithProperty(p, v)
}
