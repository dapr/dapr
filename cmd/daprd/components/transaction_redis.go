package components

import (
	"github.com/dapr/components-contrib/transaction/redis"
	transactionLoader "github.com/dapr/dapr/pkg/components/transaction"
)

func init() {
	transactionLoader.DefaultRegistry.RegisterComponent(redis.NewDistributeTransaction, "redis")
}
