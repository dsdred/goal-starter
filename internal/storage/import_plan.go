package storage

import "strings"

// Graph import classification (ADR 018 D1-D5). Every imported entity is exactly
// one of:
//
//	ImportStatusNew       — safe to create; it is an import candidate.
//	ImportStatusUpdate    — the same ID exists locally and its restorable
//	                      projection differs; the exported configuration is
//	                      restored onto that same identity.
//	ImportStatusUnchanged — the same ID exists locally and its restorable
//	                      projection is equivalent; no write is required.
//	ImportStatusBlocked   — can be applied neither safely nor unambiguously.
//
// Same ID with different content is NOT a conflict: it is the normal shape of a
// backup/restore (D3). BLOCKED is reserved for plans that cannot be applied
// safely — an entity that merely already exists is never blocked (D5).
//
// Slice 1 of ADR 018 changes classification only. The shipped plan contract
// still reports every non-created same-ID entity through the existing
// new/existing/blocked counts, and the apply step still writes only what the
// plan creates: see the transitional counting in (*ImportGraphPlan).classify.
const (
	ImportStatusNew       = "new"
	ImportStatusUpdate    = "update"
	ImportStatusUnchanged = "unchanged"
	ImportStatusBlocked   = "blocked"
)

// Blocking reason codes.
const (
	// ImportBlockedRuntimeNameTaken: the runtime name is already owned by a
	// different runtime ID. Runtime names are unique case-insensitively, so the
	// entry cannot be created; its ID differs, so there is no evidence it is the
	// same entity, and the import contract has no ID mapping mechanism.
	ImportBlockedRuntimeNameTaken = "runtime_name_taken_other_id"
	// ImportBlockedRuntimeRef: the referenced runtime exists neither in the
	// repository nor among the entities this import creates.
	ImportBlockedRuntimeRef = "runtime_ref_unresolved"
	// ImportBlockedModelRef: the referenced model exists neither in the
	// repository nor among the entities this import creates.
	ImportBlockedModelRef = "model_ref_unresolved"
	// ImportBlockedDependency: a dependency is itself blocked, so creating this
	// entity would leave a dangling reference.
	ImportBlockedDependency = "dependency_blocked"
	// ImportBlockedPipelineEntriesEmpty: creating or restoring this Pipeline
	// would leave it with no model entries, which the domain contract forbids
	// (ADR 018 D5, matching ValidatePipelineEntry). An UNCHANGED Pipeline is
	// never written, so it is never blocked by this reason.
	ImportBlockedPipelineEntriesEmpty = "pipeline_entries_empty"
)

// ImportGraphState is the repository state a plan is built against.
type ImportGraphState struct {
	Runtimes  []*RuntimeEntry
	Models    []*ModelEntry
	Pipelines []*PipelineEntry
}

// ImportCounts counts entities per type.
type ImportCounts struct {
	Runtimes  int
	Models    int
	Pipelines int
}

// Total returns the sum across entity types.
func (c ImportCounts) Total() int { return c.Runtimes + c.Models + c.Pipelines }

// ImportEntityClassification is the plan verdict for one imported entity.
type ImportEntityClassification struct {
	Type   string // runtime | model | pipeline
	ID     string
	Name   string
	Status string
	// Reason is set only for blocked entities.
	Reason string
	// RelatedID is the repository or bundle entity this classification refers to:
	// for update/unchanged entities the repository ID (identical to ID), for
	// blocked entities the colliding or unresolvable dependency.
	RelatedID string
}

// ImportGraphPlan is the outcome of classifying a bundle against repository
// state. CreateRuntimes/CreateModels/CreatePipelines hold exactly the entities
// to write; everything else is reporting metadata.
//
// Skipped is transitional: while Slice 1 changes classification only, every
// non-created same-ID entity (update or unchanged) is still reported through it,
// because the shipped plan contract and the apply step have not changed yet.
type ImportGraphPlan struct {
	Created ImportCounts
	Skipped ImportCounts
	Total   ImportCounts

	Classifications []ImportEntityClassification
	Blocked         []ImportEntityClassification

	CreateRuntimes  []*RuntimeEntry
	CreateModels    []*ModelEntry
	CreatePipelines []*PipelineEntry
}

// HasBlocked reports whether the plan contains an unsolvable conflict.
func (p *ImportGraphPlan) HasBlocked() bool { return len(p.Blocked) > 0 }

// WillCreate reports whether the plan has entities to create. Slice 1 keeps this
// as the apply and enablement predicate: an update classification does not yet
// make anything actionable, because the apply step still writes only creates.
func (p *ImportGraphPlan) WillCreate() bool {
	return p.Created.Total() > 0
}

// PlanGraphImport classifies imported entities against a repository snapshot.
//
// It is pure: it neither locks nor writes, and it never mutates state or the
// incoming entries. Identity is resolved by entity type, matching the contracts
// enforced elsewhere in this package:
//
//   - Runtime: ID is the primary identity (CreateRuntime). Name is additionally
//     unique case-insensitively, which makes same-name/different-ID impossible
//     to create and impossible to prove identical.
//   - Model, Pipeline: ID is the only identity. Names are not unique, so a
//     same-name/different-ID entry is a distinct entity and classifies as new.
//
// For an entity whose ID exists locally, the restorable projection (D11) decides
// UNCHANGED versus UPDATE; fields outside the projection are never compared.
//
// Dependencies are resolved in Runtime → Model → Pipeline order against both
// repository state and the entities this same plan creates, so a new entity may
// depend on an existing or a newly imported one, while a blocked dependency
// blocks its dependents instead of producing a dangling reference. A pending
// UPDATE satisfies a dependency through the repository ID it already holds, so
// this does not depend on bundle order either.
func PlanGraphImport(state ImportGraphState, runtimes []*RuntimeEntry, models []*ModelEntry, pipelines []*PipelineEntry) *ImportGraphPlan {
	plan := &ImportGraphPlan{}

	rtByID := make(map[string]bool, len(state.Runtimes)+len(runtimes))
	// rtByName is every name a write could collide with: repository names plus the
	// names this plan creates. localRtByName is the repository half of it alone,
	// which is the ownership ADR 018 D5 makes a rename-restore conflict.
	rtByName := make(map[string]string, len(state.Runtimes)+len(runtimes))
	localRtByName := make(map[string]string, len(state.Runtimes))
	localRuntimes := make(map[string]*RuntimeEntry, len(state.Runtimes))
	for _, e := range state.Runtimes {
		rtByID[e.ID] = true
		rtByName[strings.ToLower(e.Name)] = e.ID
		localRtByName[strings.ToLower(e.Name)] = e.ID
		localRuntimes[e.ID] = e
	}
	mByID := make(map[string]bool, len(state.Models)+len(models))
	localModels := make(map[string]*ModelEntry, len(state.Models))
	for _, e := range state.Models {
		mByID[e.ID] = true
		localModels[e.ID] = e
	}
	pByID := make(map[string]bool, len(state.Pipelines)+len(pipelines))
	localPipelines := make(map[string]*PipelineEntry, len(state.Pipelines))
	for _, e := range state.Pipelines {
		pByID[e.ID] = true
		localPipelines[e.ID] = e
	}

	// Bundle-side status index, used to tell "dependency is blocked" apart from
	// "dependency does not exist anywhere".
	bundleRuntimes := make(map[string]string, len(runtimes))
	bundleModels := make(map[string]string, len(models))

	// pipelineDependencyBlock reports the first entry reference of p that neither
	// the repository nor this plan resolves, if any.
	pipelineDependencyBlock := func(p *PipelineEntry) (string, string, bool) {
		for _, e := range p.Models {
			if mByID[e.ModelID] {
				continue
			}
			if bundleModels[e.ModelID] == ImportStatusBlocked {
				return ImportBlockedDependency, e.ModelID, true
			}
			return ImportBlockedModelRef, e.ModelID, true
		}
		return "", "", false
	}

	classify := func(c ImportEntityClassification) {
		plan.increment(&plan.Total, c.Type)
		plan.Classifications = append(plan.Classifications, c)
		switch c.Status {
		case ImportStatusNew:
			plan.increment(&plan.Created, c.Type)
		case ImportStatusUpdate, ImportStatusUnchanged:
			// Transitional (ADR 018 Slice 1): the shipped contract still reports a
			// same-ID entity as existing/skipped, and the apply step still writes
			// only what the plan creates. Slice 2' replaces this with the
			// update/unchanged counts of the plan contract.
			plan.increment(&plan.Skipped, c.Type)
		case ImportStatusBlocked:
			plan.Blocked = append(plan.Blocked, c)
		}
	}

	for _, rt := range runtimes {
		c := ImportEntityClassification{Type: "runtime", ID: rt.ID, Name: rt.Name}
		if rtByID[rt.ID] {
			c.RelatedID = rt.ID
			// ADR 018 D5/D-1: the conflict is against repository ownership, so it is
			// looked up in localRtByName only — a name another bundle entry intends
			// to write claims nothing. Both Runtimes of a name swap therefore block
			// whatever their order in the bundle.
			owner := localRtByName[strings.ToLower(rt.Name)]
			switch local := localRuntimes[rt.ID]; {
			case local == nil:
				// The ID was claimed earlier in this same bundle. Bundle validation
				// rejects duplicate ids (`portable.go:169`), so this is reachable only
				// through a direct planner call, and there is nothing to write twice.
				c.Status = ImportStatusUnchanged
			case runtimeProjectionEqual(rt, local):
				c.Status = ImportStatusUnchanged
			case owner != "" && owner != rt.ID:
				// ADR 018 D5: restoring a rename onto a name owned by a different
				// Runtime ID is a conflict, even when both Runtimes rename in this
				// same plan. A pending update never releases the name it vacates, so
				// the verdict does not depend on bundle order.
				c.Status = ImportStatusBlocked
				c.Reason = ImportBlockedRuntimeNameTaken
				c.RelatedID = owner
			default:
				// The name this UPDATE would write is deliberately not reserved
				// against other bundle entries: ADR 018 Slice 1 writes no update,
				// and an intra-bundle name collision is already a bundle-validation
				// 400 (`portable.go:273-281`). Deterministic UPDATE/NEW name
				// precedence is a Slice 2' obligation (ADR 018 D-1, test
				// obligation 8).
				c.Status = ImportStatusUpdate
			}
			bundleRuntimes[rt.ID] = c.Status
			classify(c)
			continue
		}
		if owner := rtByName[strings.ToLower(rt.Name)]; owner != "" {
			c.Status = ImportStatusBlocked
			c.Reason = ImportBlockedRuntimeNameTaken
			c.RelatedID = owner
		} else {
			c.Status = ImportStatusNew
			rtByID[rt.ID] = true
			rtByName[strings.ToLower(rt.Name)] = rt.ID
			plan.CreateRuntimes = append(plan.CreateRuntimes, rt)
		}
		bundleRuntimes[rt.ID] = c.Status
		classify(c)
	}

	for _, m := range models {
		c := ImportEntityClassification{Type: "model", ID: m.ID, Name: m.Name}
		switch local := localModels[m.ID]; {
		case mByID[m.ID] && local == nil:
			// ID claimed earlier in this same bundle (`portable.go:180` rejects it,
			// so only a direct planner call reaches this); nothing to write twice.
			c.Status = ImportStatusUnchanged
			c.RelatedID = m.ID
		case mByID[m.ID]:
			c.RelatedID = m.ID
			if modelProjectionEqual(m, local) {
				c.Status = ImportStatusUnchanged
			} else {
				c.Status = ImportStatusUpdate
			}
		case rtByID[m.RuntimeID]:
			c.Status = ImportStatusNew
			mByID[m.ID] = true
			plan.CreateModels = append(plan.CreateModels, m)
		default:
			c.RelatedID = m.RuntimeID
			if bundleRuntimes[m.RuntimeID] == ImportStatusBlocked {
				c.Reason = ImportBlockedDependency
			} else {
				c.Reason = ImportBlockedRuntimeRef
			}
			c.Status = ImportStatusBlocked
		}
		bundleModels[m.ID] = c.Status
		classify(c)
	}

	// A pipeline depends on every model entry it sequences.
	for _, p := range pipelines {
		c := ImportEntityClassification{Type: "pipeline", ID: p.ID, Name: p.Name}
		if pByID[p.ID] {
			c.RelatedID = p.ID
			local := localPipelines[p.ID]
			// A Pipeline is only blocked by an empty entry list when a write would
			// leave it empty (ADR 018 D5/D6). An UNCHANGED Pipeline is never
			// written, so an already-empty local Pipeline stays UNCHANGED.
			switch {
			case local == nil:
				// ID claimed earlier in this same bundle (`portable.go:191`).
				c.Status = ImportStatusUnchanged
			case pipelineProjectionEqual(p, local):
				c.Status = ImportStatusUnchanged
			case len(p.Models) == 0:
				c.Status = ImportStatusBlocked
				c.Reason = ImportBlockedPipelineEntriesEmpty
			default:
				if reason, relatedID, blocked := pipelineDependencyBlock(p); blocked {
					c.Status = ImportStatusBlocked
					c.Reason = reason
					c.RelatedID = relatedID
				} else {
					c.Status = ImportStatusUpdate
				}
			}
			classify(c)
			continue
		}
		if reason, relatedID, blocked := pipelineDependencyBlock(p); blocked {
			c.Status = ImportStatusBlocked
			c.Reason = reason
			c.RelatedID = relatedID
		} else if len(p.Models) == 0 {
			c.Status = ImportStatusBlocked
			c.Reason = ImportBlockedPipelineEntriesEmpty
		} else {
			c.Status = ImportStatusNew
			pByID[p.ID] = true
			plan.CreatePipelines = append(plan.CreatePipelines, p)
		}
		classify(c)
	}

	return plan
}

func (p *ImportGraphPlan) increment(dst *ImportCounts, entityType string) {
	switch entityType {
	case "runtime":
		dst.Runtimes++
	case "model":
		dst.Models++
	case "pipeline":
		dst.Pipelines++
	}
}

// ClassificationsByStatus returns the plan's classifications filtered by status.
func (p *ImportGraphPlan) ClassificationsByStatus(status string) []ImportEntityClassification {
	var out []ImportEntityClassification
	for _, c := range p.Classifications {
		if c.Status == status {
			out = append(out, c)
		}
	}
	return out
}
