package transaction

import (
	transactionComponent "github.com/dapr/components-contrib/transaction"
)

var (
	actionConfirm = "Confirm"
	actionCancel  = "Cancel"
)

func ConfirmForTcc(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionRequestParam, retryTimes int) int {
	responseStatusCode := 0
	if bunchTransactionReqsParam.Type == bunchTransactionServiceInvokeType {
		responseStatusCode, err := RequestServiceInvoke(scheduleTransactionRequest.DirectMessaging, bunchTransactionReqsParam, actionConfirm, retryTimes)

		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	}
	return responseStatusCode
}

func CancelForTcc(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionRequestParam, retryTimes int) int {
	responseStatusCode := 0
	if bunchTransactionReqsParam.Type == bunchTransactionServiceInvokeType {
		responseStatusCode, err := RequestServiceInvoke(scheduleTransactionRequest.DirectMessaging, bunchTransactionReqsParam, actionCancel, retryTimes)
		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	}
	return responseStatusCode
}
