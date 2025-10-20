package nexusoperations

import (
	"context"
	"errors"
	"fmt"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	commonnexus "go.temporal.io/server/common/nexus"
	"go.temporal.io/server/service/history/hsm"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func handleSuccessfulOperationResult(
	node *hsm.Node,
	operation Operation,
	result *commonpb.Payload,
	links []*commonpb.Link,
) error {
	eventID, err := hsm.EventIDFromToken(operation.ScheduledEventToken)
	if err != nil {
		return err
	}
	event := node.AddHistoryEvent(enumspb.EVENT_TYPE_NEXUS_OPERATION_COMPLETED, func(e *historypb.HistoryEvent) {
		// We must assign to this property, linter doesn't like this.
		// nolint:revive
		e.Attributes = &historypb.HistoryEvent_NexusOperationCompletedEventAttributes{
			NexusOperationCompletedEventAttributes: &historypb.NexusOperationCompletedEventAttributes{
				ScheduledEventId: eventID,
				Result:           result,
				RequestId:        operation.RequestId,
			},
		}
		e.Links = links
	})
	return CompletedEventDefinition{}.Apply(node.Parent, event)
}

func handleOperationError(
	node *hsm.Node,
	operation Operation,
	opErr *nexus.OperationError,
) error {
	eventID, err := hsm.EventIDFromToken(operation.ScheduledEventToken)
	if err != nil {
		return err
	}
	switch opErr.State { // nolint:exhaustive
	case nexus.OperationStateFailed:
		event := node.AddHistoryEvent(enumspb.EVENT_TYPE_NEXUS_OPERATION_FAILED, func(e *historypb.HistoryEvent) {
			// We must assign to this property, linter doesn't like this.
			// nolint:revive
			e.Attributes = &historypb.HistoryEvent_NexusOperationFailedEventAttributes{
				NexusOperationFailedEventAttributes: &historypb.NexusOperationFailedEventAttributes{
					Failure:          translateFromNexusOpErrorToTemporalOpError(operation, eventID, opErr),
					ScheduledEventId: eventID,
					RequestId:        operation.RequestId,
				},
			}
		})

		return FailedEventDefinition{}.Apply(node.Parent, event)
	case nexus.OperationStateCanceled:
		event := node.AddHistoryEvent(enumspb.EVENT_TYPE_NEXUS_OPERATION_CANCELED, func(e *historypb.HistoryEvent) {
			// We must assign to this property, linter doesn't like this.
			// nolint:revive
			e.Attributes = &historypb.HistoryEvent_NexusOperationCanceledEventAttributes{
				NexusOperationCanceledEventAttributes: &historypb.NexusOperationCanceledEventAttributes{
					Failure:          translateFromNexusOpErrorToTemporalOpError(operation, eventID, opErr),
					ScheduledEventId: eventID,
					RequestId:        operation.RequestId,
				},
			}
		})

		return CanceledEventDefinition{}.Apply(node.Parent, event)
	default:
		// Both the Nexus Client and CompletionHandler reject invalid states, but just in case, we return this as a
		// transition error.
		return fmt.Errorf("unexpected operation state: %v", opErr.State)
	}
}

func translateFromNexusOpErrorToTemporalOpError(operation Operation, scheduledEventID int64, opErr *nexus.OperationError) *failurepb.Failure {
	opErrFailure := commonnexus.TemporalDefaultFailureConverter.ErrorToFailure(opErr)
	topLevelFailure := nexusOperationFailure(operation, scheduledEventID, opErrFailure.Cause)
	topLevelFailure.Message = opErrFailure.Message
	topLevelFailure.StackTrace = opErrFailure.StackTrace
	topLevelFailure.EncodedAttributes = opErrFailure.EncodedAttributes
	if opErr.State == nexus.OperationStateCanceled && topLevelFailure.GetCanceledFailureInfo() == nil {
		// Ensure that the cause is set to a CanceledFailureInfo if the operation was canceled to be consistent with how
		// cancelation is propagated across all workflow commands.
		// Also preserve the original cause if it exists.
		topLevelFailure.Cause = &failurepb.Failure{
			Message: "operation completed as canceled",
			FailureInfo: &failurepb.Failure_CanceledFailureInfo{
				CanceledFailureInfo: &failurepb.CanceledFailureInfo{},
			},
			Cause: topLevelFailure.Cause,
		}
	}
	return topLevelFailure
}

func nexusOperationFailure(operation Operation, scheduledEventID int64, cause *failurepb.Failure) *failurepb.Failure {
	return &failurepb.Failure{
		Message: "nexus operation completed unsuccessfully",
		FailureInfo: &failurepb.Failure_NexusOperationExecutionFailureInfo{
			NexusOperationExecutionFailureInfo: &failurepb.NexusOperationFailureInfo{
				Endpoint:       operation.Endpoint,
				Service:        operation.Service,
				Operation:      operation.Operation,
				OperationToken: operation.OperationToken,
				// TODO(bergundy): This field is deprecated, remove it after the 1.27 release.
				OperationId:      operation.OperationToken,
				ScheduledEventId: scheduledEventID,
			},
		},
		Cause: cause,
	}
}

// Adds a NEXUS_OPERATION_STARTED history event and sets the operation state machine to NEXUS_OPERATION_STATE_STARTED.
// Necessary if the completion is received before the start response.
func fabricateStartedEventIfMissing(
	node *hsm.Node,
	requestID string,
	operationToken string,
	startTime *timestamppb.Timestamp,
	links []*commonpb.Link,
) error {
	operation, err := hsm.MachineData[Operation](node)
	if err != nil {
		return err
	}

	// The operation was already started, ignore.
	if !TransitionStarted.Possible(operation) {
		return nil
	}

	eventID, err := hsm.EventIDFromToken(operation.ScheduledEventToken)
	if err != nil {
		return err
	}

	event := node.AddHistoryEvent(enumspb.EVENT_TYPE_NEXUS_OPERATION_STARTED, func(e *historypb.HistoryEvent) {
		e.Attributes = &historypb.HistoryEvent_NexusOperationStartedEventAttributes{
			NexusOperationStartedEventAttributes: &historypb.NexusOperationStartedEventAttributes{
				ScheduledEventId: eventID,
				OperationToken:   operationToken,
				// TODO(bergundy): Remove this fallback after the 1.27 release.
				OperationId: operationToken,
				RequestId:   requestID,
			},
		}
		e.Links = links
		if startTime != nil {
			e.EventTime = startTime
		}
	})
	return StartedEventDefinition{}.Apply(node.Parent, event)
}

func CompletionHandler(
	ctx context.Context,
	env hsm.Environment,
	ref hsm.Ref,
	requestID string,
	operationToken string,
	startTime *timestamppb.Timestamp,
	links []*commonpb.Link,
	result *commonpb.Payload,
	opErr *nexus.OperationError,
) error {
	// The initial version of the completion token did not include a request ID.
	// Only retry Access without a run ID if the request ID is not empty.
	isRetryableNotFoundErr := requestID != ""
	err := env.Access(ctx, ref, hsm.AccessWrite, func(node *hsm.Node) error {
		if err := node.CheckRunning(); err != nil {
			return err
		}
		if err := fabricateStartedEventIfMissing(node, requestID, operationToken, startTime, links); err != nil {
			return err
		}
		operation, err := hsm.MachineData[Operation](node)
		if err != nil {
			return nil
		}
		if requestID != "" && operation.RequestId != requestID {
			isRetryableNotFoundErr = false
			return serviceerror.NewNotFound("operation not found")
		}
		if opErr != nil {
			err = handleOperationError(node, operation, opErr)
		} else {
			err = handleSuccessfulOperationResult(node, operation, result, nil)
		}
		// TODO(bergundy): Remove this once the operation auto-deletes itself from the tree on completion with state
		// based replication.
		if errors.Is(err, hsm.ErrInvalidTransition) {
			isRetryableNotFoundErr = false
			return serviceerror.NewNotFound("operation not found")
		}
		return err
	})
	if errors.As(err, new(*serviceerror.NotFound)) && isRetryableNotFoundErr && ref.WorkflowKey.RunID != "" {
		// Try again without a run ID in case the original run was reset.
		ref.WorkflowKey.RunID = ""
		// VersionedTransition is for a specific run. After reset, the TransitionCount will
		// start from 1 again. Reset the TransitionCount to 0 here to fallback to old ref
		// validation logic.
		ref.StateMachineRef.MutableStateVersionedTransition = nil
		ref.StateMachineRef.MachineInitialVersionedTransition.TransitionCount = 0
		ref.StateMachineRef.MachineLastUpdateVersionedTransition.TransitionCount = 0
		return CompletionHandler(ctx, env, ref, requestID, operationToken, startTime, links, result, opErr)
	}
	return err
}
