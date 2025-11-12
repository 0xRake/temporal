package worker

import (
	"context"
	"fmt"

	"go.temporal.io/server/chasm"
	workerstatepb "go.temporal.io/server/chasm/lib/worker/gen/workerpb/v1"
)

type handler struct {
	workerstatepb.UnimplementedWorkerServiceServer
}

func newHandler() *handler {
	return &handler{}
}

func (h *handler) RecordHeartbeat(ctx context.Context, req *workerstatepb.RecordHeartbeatRequest) (*workerstatepb.RecordHeartbeatResponse, error) {
	// Validate that exactly one worker heartbeat is present
	frontendReq := req.GetFrontendRequest()
	if frontendReq == nil || len(frontendReq.GetWorkerHeartbeat()) != 1 {
		return nil, fmt.Errorf("exactly one worker heartbeat must be present in the request")
	}

	workerHeartbeat := frontendReq.GetWorkerHeartbeat()[0]

	// Try to update existing worker, or create new one if it doesn't exist
	createResp, updateResp, _, _, err := chasm.UpdateWithNewEntity(
		ctx,
		chasm.EntityKey{
			NamespaceID: req.NamespaceId,
			BusinessID:  workerHeartbeat.WorkerInstanceKey,
		},
		func(ctx chasm.MutableContext, req *workerstatepb.RecordHeartbeatRequest) (*Worker, *workerstatepb.RecordHeartbeatResponse, error) {
			// Create new worker and record heartbeat
			w := NewWorker()
			resp, err := w.recordHeartbeat(ctx, req)
			return w, resp, err
		},
		(*Worker).recordHeartbeat,
		req,
	)

	if err != nil {
		return nil, err
	}

	// Return whichever response is populated (create or update)
	if createResp != nil {
		return createResp, nil
	}
	return updateResp, nil
}
