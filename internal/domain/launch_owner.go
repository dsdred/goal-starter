package domain

// OwnerKind identifies the kind of launch initiator for admission arbitration.
type OwnerKind int

const (
	OwnerManual OwnerKind = iota
	OwnerPipeline
)

// LaunchOwner identifies the initiator of a launch for ADR 017 admission
// arbitration. It carries the full claim identity beyond the ModelID partition
// key, enabling the compatibility matrix (within-pipeline independence,
// cross-pipeline conflict, manual priority).
type LaunchOwner struct {
	Kind            OwnerKind
	PipelineID      string // empty for manual
	PipelineEntryID string // empty for manual
}

// ManualOwner is the pre-defined owner for manual/model-endpoint/autostart launches.
var ManualOwner = LaunchOwner{Kind: OwnerManual}

// PipelineOwner returns a LaunchOwner for a pipeline entry launch.
func PipelineOwner(pipelineID, entryID string) LaunchOwner {
	return LaunchOwner{Kind: OwnerPipeline, PipelineID: pipelineID, PipelineEntryID: entryID}
}
