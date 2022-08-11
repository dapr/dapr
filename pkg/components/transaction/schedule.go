package transaction

import (
	transactionComponent "github.com/dapr/components-contrib/transaction"
)

const (
	defaultState                    = 0
	stateForTrySuccess              = 1
	stateForTryFailure              = -1
	stateForConfirmSuccess          = 2
	stateForConfirmFailure          = -2
	stateForRollBackSuccess         = 3
	stateForRollBackFailure         = -3
	bunchTransactionStateParam      = "state"
	bunchTransacitonTryRequestParam = "tryRequestParam"
	requestStatusOK                 = 1
	defaultTransactionSchema        = "tcc"
)

func ConfirmTransaction(transactionInstance transactionComponent.Transaction, reqParam TransactionConfirmRequest) error {
	reqs, err := transactionInstance.GetBunchTransactions(
		transactionComponent.GetBunchTransactionsRequest{
			TransactionId: reqParam.TransactionId,
		},
	)
	if err != nil {
		return err
	}
	bunchTransactions := reqs.BunchTransactions
	for bunchTransactionId, bunchTransaction := range bunchTransactions {
		state, _ := bunchTransaction[bunchTransactionStateParam].(int)
		// pointer of the origin request param
		bunchTransactionReqsParam := bunchTransaction[bunchTransacitonTryRequestParam]
		Confirm(bunchTransactionReqsParam)
		if state != stateForConfirmSuccess {
			transactionInstance.Confirm(transactionComponent.BunchTransactionConfirmRequest{
				TransactionId:      reqParam.TransactionId,
				BunchTransactionId: bunchTransactionId,
				StatusCode:         stateForConfirmSuccess,
			})
		}
	}
	return nil

}

func Confirm(bunchTransactionReqsParam interface{}) {

}
