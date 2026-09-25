package storage

import "strings"

// Graph import classification. Every imported entity is exactly one of:
//
//	ImportStatusNew      — safe to create; it is an import candidate.
//	ImportStatusExisting — an entity with the same ID already exists; it is skipped.
//	ImportStatusBlocked  — can be neither created nor safely skipped.
//
// The policy is SKIP EXISTING: conflicting repository state is never
// overwritten, merged, updated, copied or remapped.
const (
	ImportStatusNew      = "new"
	ImportStatusExisting = "existing"
	ImportStatusBlocked  = "blocked"
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
	// for existing entities the repository ID (identical to ID), for blocked
	// entities the colliding or unresolvable dependency.
	RelatedID string
}

// ImportGraphPlan is the outcome of classifying a bundle against repository
// state. CreateRuntimes/CreateModels/CreatePipelines hold exactly the entities
// to write; everything else is reporting metadata.
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

// WillCreate reports whether the plan has anything to import.
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
// Dependencies are resolved in Runtime → Model → Pipeline order against both
// repository state and the entities this same plan creates, so a new entity may
// depend on an existing or a newly imported one, while a blocked dependency
// blocks its dependents instead of producing a dangling reference.
func PlanGraphImport(state ImportGraphState, runtimes []*RuntimeEntry, models []*ModelEntry, pipelines []*PipelineEntry) *ImportGraphPlan {
	plan := &ImportGraphPlan{}

	rtByID := make(map[string]bool, len(state.Runtimes)+len(runtimes))
	rtByName := make(map[string]string, len(state.Runtimes)+len(runtimes))
	for _, e := range state.Runtimes {
		rtByID[e.ID] = true
		rtByName[strings.ToLower(e.Name)] = e.ID
	}
	mByID := make(map[string]bool, len(state.Models)+len(models))
	for _, e := range state.Models {
		mByID[e.ID] = true
	}
	pByID := make(map[string]bool, len(state.Pipelines)+len(pipelines))
	for _, e := range state.Pipelines {
		pByID[e.ID] = true
	}

	// Bundle-side status index, used to tell "dependency is blocked" apart from
	// "dependency does not exist anywhere".
	bundleRuntimes := make(map[string]string, len(runtimes))
	bundleModels := make(map[string]string, len(models))

	classify := func(c ImportEntityClassification) {
		plan.increment(&plan.Total, c.Type)
		plan.Classifications = append(plan.Classifications, c)
		switch c.Status {
		case ImportStatusNew:
			plan.increment(&plan.Created, c.Type)
		case ImportStatusExisting:
			plan.increment(&plan.Skipped, c.Type)
		case ImportStatusBlocked:
			plan.Blocked = append(plan.Blocked, c)
		}
	}

	for _, rt := range runtimes {
		c := ImportEntityClassification{Type: "runtime", ID: rt.ID, Name: rt.Name}
		switch {
		case rtByID[rt.ID]:
			c.Status = ImportStatusExisting
			c.RelatedID = rt.ID
		case rtByName[strings.ToLower(rt.Name)] != "":
			c.Status = ImportStatusBlocked
			c.Reason = ImportBlockedRuntimeNameTaken
			c.RelatedID = rtByName[strings.ToLower(rt.Name)]
		default:
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
		switch {
		case mByID[m.ID]:
			c.Status = ImportStatusExisting
			c.RelatedID = m.ID
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
			c.Status = ImportStatusExisting
			c.RelatedID = p.ID
			classify(c)
			continue
		}
		blocked := false
		for _, e := range p.Models {
			if mByID[e.ModelID] {
				continue
			}
			c.RelatedID = e.ModelID
			if bundleModels[e.ModelID] == ImportStatusBlocked {
				c.Reason = ImportBlockedDependency
			} else {
				c.Reason = ImportBlockedModelRef
			}
			blocked = true
			break
		}
		if blocked {
			c.Status = ImportStatusBlocked
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
