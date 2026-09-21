package application

import (
	"errors"
	"fmt"

	"github.com/dsdred/goal/internal/process"
)

// PipelineFailureReason is the application-owned class of one group lifecycle
// failure (BF-02). It carries no HTTP meaning on purpose: status and
// client-visible token are the webui layer's decision (BF-03a), and the
// classification below is by sentinel identity, never by message text.
type PipelineFailureReason string

// Frozen reason vocabulary. ReasonStopFailed ("stop-failed", declared with the
// other outcome reasons in pipeline_service.go) stays the catch-all class.
const (
	ReasonLaunchInFlight         PipelineFailureReason = "launch-in-flight"
	ReasonInstanceGone           PipelineFailureReason = "instance-gone"
	ReasonPersistenceFailed      PipelineFailureReason = "persistence-failed"
	ReasonTerminationUnconfirmed PipelineFailureReason = "termination-unconfirmed"
	ReasonShuttingDown           PipelineFailureReason = "shutting-down"
)

// Group lifecycle phases, used to attribute an aggregate to its phase.
const (
	PhaseStart   = "start"
	PhaseStop    = "stop"
	PhaseRestart = "restart"
)

// PipelineFailure is one per-instance failure of a best-effort group
// lifecycle request: which entry, which instance, and the bounded class. It is
// metadata only — no response payload, no status, no transport type — so the
// handler can attribute a failure to an entry without the application layer
// knowing how the response is assembled.
type PipelineFailure struct {
	Phase      string
	EntryID    string
	Index      int
	ModelID    string
	InstanceID string
	Reason     PipelineFailureReason
}

// PipelineStopError reports that a group stop or restart left at least one
// owned instance in a non-stopped state (BF-02a: the HTTP status used to be 200
// no matter how many instances stayed running).
//
// It is an ATTRIBUTION error, not a rollback and not a response: the per-entry
// body stays the authoritative result returned alongside it, instances that
// already stopped are never rewound, and this error holds no copy or reference
// of that body. errs preserves every underlying failure so callers classify the
// aggregate with errors.Is/errors.As over the process-layer sentinels.
type PipelineStopError struct {
	PipelineID string
	Phase      string // PhaseStop | PhaseRestart
	Failures   []PipelineFailure

	errs []error
}

func (e *PipelineStopError) Error() string {
	return fmt.Sprintf("pipeline %s incomplete: %d failure(s) for pipeline %s",
		e.Phase, len(e.Failures), e.PipelineID)
}

// Unwrap exposes every preserved underlying error of the aggregate.
func (e *PipelineStopError) Unwrap() []error { return e.errs }

// PipelineStartError reports that a group start was refused by system shutdown
// (BF-02e) — the one start outcome that is not a reportable business result.
// Ordinary best-effort start outcomes stay per-entry and produce no error, so
// the request keeps its 200 body (ADR 010 acceptance 5).
//
// Like PipelineStopError it holds failure metadata only, never the response.
type PipelineStartError struct {
	PipelineID string
	Phase      string // always PhaseStart
	Failures   []PipelineFailure

	errs []error
}

func (e *PipelineStartError) Error() string {
	return fmt.Sprintf("pipeline %s incomplete: %d failure(s) for pipeline %s",
		e.Phase, len(e.Failures), e.PipelineID)
}

// Unwrap exposes every preserved underlying error of the aggregate.
func (e *PipelineStartError) Unwrap() []error { return e.errs }

// lifecycleCollector accumulates the failures of one group request in execution
// order: stop phase in reverse entry order, then start phase in forward order.
type lifecycleCollector struct {
	failures []PipelineFailure
	errs     []error
}

func (c *lifecycleCollector) add(failure PipelineFailure, err error) {
	c.failures = append(c.failures, failure)
	if err != nil {
		c.errs = append(c.errs, err)
	}
}

// stopAggregate returns the stop/restart attribution error, or nil when nothing
// failed. It never touches the response body.
func (c *lifecycleCollector) stopAggregate(phase, pipelineID string) error {
	if len(c.failures) == 0 {
		return nil
	}
	return &PipelineStopError{
		PipelineID: pipelineID,
		Phase:      phase,
		Failures:   c.failures,
		errs:       c.errs,
	}
}

// startAggregate returns the start attribution error, or nil when no entry was
// refused by shutdown.
func (c *lifecycleCollector) startAggregate(pipelineID string) error {
	if len(c.failures) == 0 {
		return nil
	}
	return &PipelineStartError{
		PipelineID: pipelineID,
		Phase:      PhaseStart,
		Failures:   c.failures,
		errs:       c.errs,
	}
}

// classifyStopFailure maps one Supervisor stop error to its bounded reason
// class by sentinel identity.
func classifyStopFailure(err error) PipelineFailureReason {
	switch {
	case errors.Is(err, process.ErrInstanceNotFound):
		return ReasonInstanceGone
	case errors.Is(err, process.ErrLaunchInFlight):
		return ReasonLaunchInFlight
	case errors.Is(err, process.ErrPersistenceFailure):
		return ReasonPersistenceFailed
	case errors.Is(err, process.ErrTerminationUnconfirmed):
		return ReasonTerminationUnconfirmed
	case isShutdownClass(err):
		return ReasonShuttingDown
	default:
		return ReasonStopFailed
	}
}

// isShutdownClass reports whether the error (or any error joined into it,
// including an admission rejection) is a shutdown condition: either shutdown
// won before admission (AdmissionRejection{RejShuttingDown}) or the admitted
// launch was aborted pre-spawn by shutdown (ADR 017 RB-015b).
func isShutdownClass(err error) bool {
	return errors.Is(err, process.ErrLaunchAbortedByShutdown) ||
		process.HasRejectionReason(err, process.RejShuttingDown)
}
