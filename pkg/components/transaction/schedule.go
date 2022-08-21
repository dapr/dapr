package transaction

import (
	"context"
	"fmt"

	transactionComponent "github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/kit/logger"
	fasthttp "github.com/valyala/fasthttp"
	codes "google.golang.org/grpc/codes"
)

var (
	// defaultState                      = 0
	stateForTrySuccess = 10
	// stateForTryFailure                = 1
	stateForConfirmSuccess = 20
	// stateForConfirmFailure            = 2
	stateForRollbackSuccess = 30
	stateForRollbackFailure = 3
	// requestStatusOK                   = 1
	bunchTransactionServiceInvokeType = "service-invoke"
	bunchTransactionActorType         = "actor"
	transactionConfirm                = "Confirm"
	transactionRollback               = "Rollback"
	log                               = logger.NewLogger("dapr.components.transaction")
)

func ConfirmTransaction(scheduleTransactionRequest ScheduleTransactionRequest) error {
	transactionInstance := scheduleTransactionRequest.TransactionInstance
	reqs, err := transactionInstance.GetBunchTransactions(
		transactionComponent.GetBunchTransactionsRequest{
			TransactionID: scheduleTransactionRequest.TransactionID,
		},
	)
	if err != nil {
		log.Debug(err)
		return err
	}
	bunchTransactions := reqs.BunchTransactions
	retryTimes := transactionInstance.GetRetryTimes()
	schema := transactionInstance.GetTransactionSchema()

	log.Debugf("disrtibute transaction schema is %s :", schema)

	allBunchTransactionConfirmSuccess := true

	for bunchTransactionID, bunchTransaction := range bunchTransactions {
		state := bunchTransaction.StatusCode
		// data of the origin request param
		bunchTransactionReqsParam := bunchTransaction.TryRequestParam
		if state == stateForTrySuccess {
			// try to confirm a bunch transaction
			responseStatusCode := Confirm(scheduleTransactionRequest, bunchTransactionReqsParam, schema, retryTimes)

			if responseStatusCode != fasthttp.StatusOK {
				allBunchTransactionConfirmSuccess = false
				break
			}

			transactionInstance.Confirm(transactionComponent.BunchTransactionConfirmRequest{
				TransactionID:      scheduleTransactionRequest.TransactionID,
				BunchTransactionID: bunchTransactionID,
				StatusCode:         stateForConfirmSuccess,
			})
		}
	}

	// all bunch transaction confirm success, release state resource
	if allBunchTransactionConfirmSuccess {
		transactionInstance.ReleaseTransactionResource(transactionComponent.ReleaseTransactionRequest{
			TransactionID: scheduleTransactionRequest.TransactionID,
		})
	} else {
		return fmt.Errorf("fail to confirm distribute transaction")
	}

	return nil

}

func RollbackAction(scheduleTransactionRequest ScheduleTransactionRequest) error {
	transactionInstance := scheduleTransactionRequest.TransactionInstance
	reqs, err := transactionInstance.GetBunchTransactions(
		transactionComponent.GetBunchTransactionsRequest{
			TransactionID: scheduleTransactionRequest.TransactionID,
		},
	)
	if err != nil {
		log.Debug(err)
		return err
	}
	bunchTransactions := reqs.BunchTransactions
	retryTimes := transactionInstance.GetRetryTimes()
	schema := transactionInstance.GetTransactionSchema()

	log.Debugf("disrtibute transaction schema is %s :", schema)

	allBunchTransactionRollbackSuccess := true

	for bunchTransactionID, bunchTransaction := range bunchTransactions {
		state := bunchTransaction.StatusCode
		// data of the origin request param
		bunchTransactionReqsParam := bunchTransaction.TryRequestParam
		if state == stateForTrySuccess {
			// try to confirm a bunch transaction
			responseStatusCode := Rollback(scheduleTransactionRequest, bunchTransactionReqsParam, schema, retryTimes)

			if responseStatusCode != fasthttp.StatusOK {
				allBunchTransactionRollbackSuccess = false
				transactionInstance.Rollback(transactionComponent.BunchTransactionRollbackRequest{
					TransactionID:      scheduleTransactionRequest.TransactionID,
					BunchTransactionID: bunchTransactionID,
					StatusCode:         stateForRollbackFailure,
				})
			} else {
				transactionInstance.Rollback(transactionComponent.BunchTransactionRollbackRequest{
					TransactionID:      scheduleTransactionRequest.TransactionID,
					BunchTransactionID: bunchTransactionID,
					StatusCode:         stateForRollbackSuccess,
				})
			}

		}
	}

	// all bunch transaction confirm success, release state resource
	if allBunchTransactionRollbackSuccess {
		transactionInstance.ReleaseTransactionResource(transactionComponent.ReleaseTransactionRequest{
			TransactionID: scheduleTransactionRequest.TransactionID,
		})
	} else {
		return fmt.Errorf("fail to rollback all of bunch distribute transaction")
	}

	return nil
}

func Confirm(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, schema string, retryTimes int) int {
	log.Debug("switch to a Confrim action with : ", bunchTransactionReqsParam)
	responseStatusCode := 0
	switch schema {
	case "tcc":
		responseStatusCode = ConfirmTcc(scheduleTransactionRequest, bunchTransactionReqsParam, retryTimes)
	}
	return responseStatusCode
}

func Rollback(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, schema string, retryTimes int) int {
	log.Debug("switch to a Rollback action with : ", bunchTransactionReqsParam)
	responseStatusCode := 0
	switch schema {
	case "tcc":
		responseStatusCode = RollbackTcc(scheduleTransactionRequest, bunchTransactionReqsParam, retryTimes)
	}
	return responseStatusCode
}

func ConfirmTcc(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, retryTimes int) int {
	responseStatusCode := 0
	if bunchTransactionReqsParam.Type == bunchTransactionServiceInvokeType {
		responseStatusCode, err := RequestServiceInovde(scheduleTransactionRequest.DirectMessaging, bunchTransactionReqsParam, transactionConfirm, retryTimes)

		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	} else if bunchTransactionReqsParam.Type == bunchTransactionActorType {
		responseStatusCode, err := RequestActor(scheduleTransactionRequest.Actor, bunchTransactionReqsParam, transactionConfirm, retryTimes)
		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	}

	return responseStatusCode
}

func RollbackTcc(scheduleTransactionRequest ScheduleTransactionRequest, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, retryTimes int) int {
	responseStatusCode := 0
	if bunchTransactionReqsParam.Type == bunchTransactionServiceInvokeType {
		responseStatusCode, err := RequestServiceInovde(scheduleTransactionRequest.DirectMessaging, bunchTransactionReqsParam, transactionRollback, retryTimes)
		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	} else if bunchTransactionReqsParam.Type == bunchTransactionActorType {
		responseStatusCode, err := RequestActor(scheduleTransactionRequest.Actor, bunchTransactionReqsParam, transactionRollback, retryTimes)
		if err != nil {
			log.Debug(err)
		}
		return responseStatusCode
	}

	return responseStatusCode
}

func RequestServiceInovde(directMessaging messaging.DirectMessaging, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, action string, retryTimes int) (int, error) {
	req := invokev1.NewInvokeMethodRequest(bunchTransactionReqsParam.InvokeMethodName+action).WithHTTPExtension(bunchTransactionReqsParam.Verb, bunchTransactionReqsParam.QueryArgs)

	req.WithRawData(bunchTransactionReqsParam.Data, bunchTransactionReqsParam.ContentType)
	// Save headers to internal metadata
	req.WithFastHTTPHeaders(bunchTransactionReqsParam.Header)

	ctx := context.Background()
	i := 1
	for i <= retryTimes {
		i++
		resp, err := directMessaging.Invoke(ctx, bunchTransactionReqsParam.TargetID, req)

		if err != nil {
			return 0, err
		}
		// Construct response
		statusCode := int(resp.Status().Code)
		if !resp.IsHTTPResponse() {
			statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
			if statusCode != fasthttp.StatusOK {
				continue
			}
		}
		if statusCode == fasthttp.StatusOK {
			return statusCode, nil
		} else {
			continue
		}

	}
	return 0, nil
}

func RequestActor(actor actors.Actors, bunchTransactionReqsParam *transactionComponent.TransactionTryRequestParam, action string, retryTimes int) (int, error) {

	req := invokev1.NewInvokeMethodRequest(bunchTransactionReqsParam.InvokeMethodName + action)
	req.WithActor(bunchTransactionReqsParam.ActorType, bunchTransactionReqsParam.ActorID)
	req.WithHTTPExtension(bunchTransactionReqsParam.Verb, bunchTransactionReqsParam.QueryArgs)
	req.WithRawData(bunchTransactionReqsParam.Data, bunchTransactionReqsParam.ContentType)

	// Save headers to metadata.
	metadata := map[string][]string{}
	header := bunchTransactionReqsParam.Header
	header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})
	req.WithMetadata(metadata)

	ctx := context.Background()
	i := 1
	for i <= retryTimes {
		i++
		resp, err := actor.Call(ctx, req)
		if err != nil {
			return 0, err
		}
		statusCode := int(resp.Status().Code)

		if !resp.IsHTTPResponse() {
			statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
		}
		if statusCode == fasthttp.StatusOK {
			return statusCode, nil
		} else {
			continue
		}
	}

	return 0, nil
}
