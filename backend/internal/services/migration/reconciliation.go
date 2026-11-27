package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"hello-world/internal/services/authz"
)

// TransactionEvidence identifies the original transaction on a specific target.
type TransactionEvidence struct {
	XactID         string
	TargetIdentity string
}

type TransactionOutcome string

const (
	TransactionCommitted TransactionOutcome = "committed"
	TransactionAborted   TransactionOutcome = "aborted"
)

type targetTransactionRecorder interface {
	RecordTransactionEvidence(ctx context.Context) (TransactionEvidence, error)
}

type targetTransactionVerifier interface {
	VerifyTransaction(ctx context.Context, databaseID string, evidence TransactionEvidence) (TransactionOutcome, error)
}

type ReconcileResult struct {
	ExecutionID string    `json:"execution_id"`
	Status      RunStatus `json:"status"`
}

// ReconcileRun finalizes the existing platform record using a proven target outcome.
func (s *Service) ReconcileRun(ctx context.Context, principal Principal, databaseID string, runID int64) (ReconcileResult, error) {
	if err := s.authorize(ctx, principal, "", authz.ManageConnections); err != nil {
		return ReconcileResult{}, err
	}
	if strings.TrimSpace(databaseID) == "" || runID <= 0 {
		return ReconcileResult{}, ErrNotFound
	}
	run, evidence, err := s.store.GetRunForReconciliation(ctx, databaseID, runID)
	if err != nil {
		return ReconcileResult{}, err
	}
	result := ReconcileResult{ExecutionID: strconv.FormatInt(run.ID, 10), Status: run.Status}
	if run.Status != RunStatusRunning {
		return result, nil
	}
	verifier, ok := s.connector.(targetTransactionVerifier)
	if !ok || evidence.XactID == "" || evidence.TargetIdentity == "" {
		return ReconcileResult{}, ErrOutcomeUnconfirmed
	}
	outcome, err := verifier.VerifyTransaction(ctx, databaseID, evidence)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("%w: verifying target transaction for run %d: %w", ErrOutcomeUnconfirmed, run.ID, err)
	}

	finishedAt := s.nowFn()
	var version *int64
	switch outcome {
	case TransactionCommitted:
		assignedVersion, finishErr := s.store.CompleteRunSucceeded(ctx, run.ID, run.MigrationID, databaseID, run.ExecutionMs, finishedAt)
		err = finishErr
		version = &assignedVersion
		result.Status = RunStatusSucceeded
	case TransactionAborted:
		err = s.store.CompleteRunFailed(ctx, run.ID, run.MigrationID, "PostgreSQL confirmed that the target transaction was aborted", run.ExecutionMs, finishedAt)
		result.Status = RunStatusFailed
	default:
		return ReconcileResult{}, ErrOutcomeUnconfirmed
	}
	if err != nil {
		// The original executor or another operator may have finalized this run.
		current, _, readErr := s.store.GetRunForReconciliation(ctx, databaseID, runID)
		if readErr == nil && current.Status != RunStatusRunning {
			result.Status = current.Status
			return result, nil
		}
		return ReconcileResult{}, fmt.Errorf("%w: finalizing reconciled run %d: %w", ErrOutcomeUnconfirmed, run.ID, err)
	}

	s.logAudit(ctx, AuditEvent{
		ActorUserID: principal.UserID, Action: "migration_attempt_reconciled",
		ObjectType: "migration", ObjectID: run.MigrationID, DatabaseID: databaseID,
		Status: string(result.Status), CreatedAt: finishedAt, ExecutionID: run.ID,
		Operation: run.Operation, AttemptNumber: run.AttemptNumber, Revision: run.Revision,
		Version: version, DurationMs: run.ExecutionMs,
	})
	return result, nil
}
