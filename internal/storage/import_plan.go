package storage

import (
	"sort"
	"strings"
)

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
// UPDATE and UNCHANGED are decisions about the write, not only about the report:
// CreateRuntimes/CreateModels/CreatePipelines hold the entities to append and
// UpdateRuntimes/UpdateModels/UpdatePipelines hold the restore records that
// replace an existing record in place (ADR 018 D3/D11; see ImportGraph).
const (
	ImportStatusNew       = "new"
	ImportStatusUpdate    = "update"
	ImportStatusUnchanged = "unchanged"
	ImportStatusBlocked   = "blocked"
)

// Blocking reason codes.
const (
	// ImportBlockedRuntimeNameTaken: one case-insensitive final runtime name is
	// claimed by two different runtimes. The claim set is the repository's current
	// names plus the intended final name of every imported runtime that would
	// write, so it covers a rename onto a locally-owned name (D-1), a NEW runtime
	// against a pending UPDATE (D-8), and two NEW runtimes (D-12).
	ImportBlockedRuntimeNameTaken = "runtime_name_taken_other_id"
	// ImportBlockedRuntimeRef: the referenced runtime exists neither in the
	// repository nor among the entities this import creates.
	ImportBlockedRuntimeRef = "runtime_ref_unresolved"
	// ImportBlockedModelRef: the referenced model exists neither in the
	// repository nor among the entities this import creates.
	ImportBlockedModelRef = "model_ref_unresolved"
	// ImportBlockedDependency: a dependency is itself blocked, so writing this
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
// state. Create*/Update* hold exactly the entities to write; everything else is
// reporting metadata. An UNCHANGED or BLOCKED entity appears in neither list,
// because restore writes nothing for it.
type ImportGraphPlan struct {
	Created   ImportCounts
	Updated   ImportCounts
	Unchanged ImportCounts
	Total     ImportCounts

	Classifications []ImportEntityClassification
	Blocked         []ImportEntityClassification

	CreateRuntimes  []*RuntimeEntry
	CreateModels    []*ModelEntry
	CreatePipelines []*PipelineEntry

	// UpdateRuntimes/UpdateModels/UpdatePipelines hold restore records: the local
	// record with the bundle's restorable projection written onto a copy, so the
	// fields restore must never touch (Environment values, CreatedAt, the in-memory
	// model PipelineID/PipelineEntryID) stay exactly as stored (ADR 018 D7/D10/D11).
	UpdateRuntimes  []*RuntimeEntry
	UpdateModels    []*ModelEntry
	UpdatePipelines []*PipelineEntry
}

// HasBlocked reports whether the plan contains an unsolvable conflict.
func (p *ImportGraphPlan) HasBlocked() bool { return len(p.Blocked) > 0 }

// WillMutate reports whether the plan has anything to write: NEW + UPDATE > 0
// (ADR 018 D-9). It is both the repository apply predicate and, together with
// HasBlocked, the import enablement predicate — an all-UNCHANGED plan writes
// nothing and must leave the durable file byte-identical.
func (p *ImportGraphPlan) WillMutate() bool {
	return p.Created.Total()+p.Updated.Total() > 0
}

// markBlocked re-statuses one planned entity to BLOCKED and keeps the per-type
// counts coherent, so a plan body reported alongside a conflict never claims a
// write it did not perform. It returns false when the plan holds no such
// classification, which is the case for an entity that comes from repository
// state rather than from the bundle; the plan totals only ever count bundle
// entities, so such an entity has no classification to re-status.
func (p *ImportGraphPlan) markBlocked(entityType, id, reason, relatedID string) bool {
	for i := range p.Classifications {
		c := &p.Classifications[i]
		if c.Type != entityType || c.ID != id || c.Status == ImportStatusBlocked {
			continue
		}
		switch c.Status {
		case ImportStatusNew:
			p.decrement(&p.Created, c.Type)
		case ImportStatusUpdate:
			p.decrement(&p.Updated, c.Type)
		case ImportStatusUnchanged:
			p.decrement(&p.Unchanged, c.Type)
		}
		c.Status = ImportStatusBlocked
		c.Reason = reason
		c.RelatedID = relatedID
		p.Blocked = append(p.Blocked, *c)
		return true
	}
	return false
}

// addEscapedBlock records a BLOCKED verdict produced by the repository's
// pre-write invariant rather than by the planner. When the entity is a bundle
// entity its classification is re-statused so the per-type counts stay coherent;
// when it is not — a collision between two records the repository already holds —
// it is still reported, because a D-15 conflict body MUST name every collided
// Runtime and MUST report can_import == false through the single D-9 predicate.
func (p *ImportGraphPlan) addEscapedBlock(entityType, id, name, reason, relatedID string) {
	if p.markBlocked(entityType, id, reason, relatedID) {
		return
	}
	p.Blocked = append(p.Blocked, ImportEntityClassification{
		Type:      entityType,
		ID:        id,
		Name:      name,
		Status:    ImportStatusBlocked,
		Reason:    reason,
		RelatedID: relatedID,
	})
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
// blocks its dependents instead of producing a dangling reference. Because an
// UPDATE now writes, a pending UPDATE's own references are resolved too: an
// entity that would be written with an unresolvable reference is blocked
// (ADR 018 D12). An UNCHANGED entity is never checked — nothing is written for
// it, so it can leave no dangling reference behind (D5/D-6).
//
// Runtime name contest (ADR 018 D-1/D-8/D-12/D-13): a case-insensitive final
// runtime name claimed by two different runtimes blocks every claimant, with no
// precedence and no first-wins for any class. The claim set is built in one pass
// from facts that do not depend on any verdict: every repository Runtime holds
// its current name (D-1 — a pending UPDATE never releases the name it vacates),
// and every imported Runtime that would write contributes its intended final
// name, which is a NEW entry or a same-ID entry whose projection differs. An
// entry that writes nothing claims nothing, and a model or pipeline blocked by a
// reference is not a Runtime at all, so a blocked entity never manufactures a
// collision (D-13). Each runtime therefore sits in exactly one name group, and
// the verdict is identical for every permutation of the same inputs. A group that
// collapses to one Runtime ID is not a conflict: a Runtime never conflicts with
// itself (D-8). Only an entity that would write can be BLOCKED by the contest — a
// Runtime that holds the name as an unchanged repository fact reserves it against
// others but is itself no operation to fail.
//
// A contested intended name is reachable at this level only — a bundle carrying
// two Runtime entries with one case-insensitive name is rejected with 400 by
// bundle validation before planning, and that boundary stays.
func PlanGraphImport(state ImportGraphState, runtimes []*RuntimeEntry, models []*ModelEntry, pipelines []*PipelineEntry) *ImportGraphPlan {
	plan := &ImportGraphPlan{}

	localRuntimes := make(map[string]*RuntimeEntry, len(state.Runtimes))
	for _, e := range state.Runtimes {
		localRuntimes[e.ID] = e
	}
	localModels := make(map[string]*ModelEntry, len(state.Models))
	for _, e := range state.Models {
		localModels[e.ID] = e
	}
	localPipelines := make(map[string]*PipelineEntry, len(state.Pipelines))
	for _, e := range state.Pipelines {
		localPipelines[e.ID] = e
	}

	classify := func(c ImportEntityClassification) {
		plan.increment(&plan.Total, c.Type)
		plan.Classifications = append(plan.Classifications, c)
		switch c.Status {
		case ImportStatusNew:
			plan.increment(&plan.Created, c.Type)
		case ImportStatusUpdate:
			plan.increment(&plan.Updated, c.Type)
		case ImportStatusUnchanged:
			plan.increment(&plan.Unchanged, c.Type)
		case ImportStatusBlocked:
			plan.Blocked = append(plan.Blocked, c)
		}
	}

	// ─── Runtime name contest ───

	// intent is what a bundle runtime entry would do to the graph, decided without
	// consulting any verdict.
	const (
		rtCreate = iota
		rtRestore
		rtNoWrite
	)
	intent := make([]int, len(runtimes))
	// nameHolders maps one case-insensitive runtime name to every different runtime
	// ID that holds it: repository records first (their current names are reserved),
	// then the intended final name of each entry that would write.
	nameHolders := make(map[string]map[string]bool, len(state.Runtimes)+len(runtimes))
	addHolder := func(name, id string) {
		k := strings.ToLower(name)
		if nameHolders[k] == nil {
			nameHolders[k] = make(map[string]bool, 2)
		}
		nameHolders[k][id] = true
	}
	for _, e := range state.Runtimes {
		addHolder(e.Name, e.ID)
	}
	bundleRuntimeID := make(map[string]bool, len(runtimes))
	for i, rt := range runtimes {
		local := localRuntimes[rt.ID]
		switch {
		case local == nil && bundleRuntimeID[rt.ID]:
			// The ID was claimed earlier in this same bundle. Bundle validation
			// rejects duplicate ids, so this is reachable only through a direct
			// planner call, and there is nothing to write twice.
			intent[i] = rtNoWrite
		case local == nil:
			bundleRuntimeID[rt.ID] = true
			intent[i] = rtCreate
		case runtimeProjectionEqual(rt, local):
			intent[i] = rtNoWrite
		default:
			intent[i] = rtRestore
		}
		if intent[i] != rtNoWrite {
			addHolder(rt.Name, rt.ID)
		}
	}

	// contested reports whether the name has more than one holder, and names the
	// lexicographically smallest OTHER holder as the reported counterpart
	// (ADR 018 D-14).
	contested := func(name, id string) (bool, string) {
		holders := nameHolders[strings.ToLower(name)]
		if len(holders) < 2 {
			return false, ""
		}
		return true, smallestOther(holders, id)
	}

	// createdRuntimes resolves a dependency only through a runtime the graph holds
	// after this plan: a repository record — including one whose own UPDATE is
	// blocked, since blocking never removes it — or a NEW runtime this plan creates.
	createdRuntimes := make(map[string]bool, len(runtimes))
	bundleRuntimes := make(map[string]string, len(runtimes))
	for i, rt := range runtimes {
		c := ImportEntityClassification{Type: "runtime", ID: rt.ID, Name: rt.Name}
		// Only an entity that would write can be blocked by the contest (D-13): an
		// UNCHANGED Runtime holds a name as a repository fact, which reserves that
		// name against others but is itself no operation to fail.
		if blocked, relatedID := contested(rt.Name, rt.ID); blocked && intent[i] != rtNoWrite {
			c.Status = ImportStatusBlocked
			c.Reason = ImportBlockedRuntimeNameTaken
			c.RelatedID = relatedID
		} else {
			switch intent[i] {
			case rtCreate:
				c.Status = ImportStatusNew
				createdRuntimes[rt.ID] = true
				plan.CreateRuntimes = append(plan.CreateRuntimes, rt)
			case rtRestore:
				c.Status = ImportStatusUpdate
				c.RelatedID = rt.ID
				plan.UpdateRuntimes = append(plan.UpdateRuntimes, restoredRuntime(localRuntimes[rt.ID], rt))
			default:
				c.Status = ImportStatusUnchanged
				c.RelatedID = rt.ID
			}
		}
		bundleRuntimes[rt.ID] = c.Status
		classify(c)
	}

	// ─── Models ───

	createdModels := make(map[string]bool, len(models))
	runtimeResolves := func(id string) bool {
		return localRuntimes[id] != nil || createdRuntimes[id]
	}
	// modelReferenceBlock reports the first runtime reference of m that the final
	// graph cannot resolve, if any.
	modelReferenceBlock := func(m *ModelEntry) (string, string, bool) {
		if runtimeResolves(m.RuntimeID) {
			return "", "", false
		}
		if bundleRuntimes[m.RuntimeID] == ImportStatusBlocked {
			return ImportBlockedDependency, m.RuntimeID, true
		}
		return ImportBlockedRuntimeRef, m.RuntimeID, true
	}
	bundleModels := make(map[string]string, len(models))
	for _, m := range models {
		c := ImportEntityClassification{Type: "model", ID: m.ID, Name: m.Name}
		local := localModels[m.ID]
		switch {
		case local == nil && createdModels[m.ID]:
			// ID claimed earlier in this same bundle; nothing to write twice.
			c.Status = ImportStatusUnchanged
			c.RelatedID = m.ID
		case local != nil:
			c.RelatedID = m.ID
			if modelProjectionEqual(m, local) {
				// Nothing is written, so no reference of this entity can dangle.
				c.Status = ImportStatusUnchanged
			} else if reason, relatedID, blocked := modelReferenceBlock(m); blocked {
				c.Status = ImportStatusBlocked
				c.Reason = reason
				c.RelatedID = relatedID
			} else {
				c.Status = ImportStatusUpdate
				plan.UpdateModels = append(plan.UpdateModels, restoredModel(local, m))
			}
		case runtimeResolves(m.RuntimeID):
			c.Status = ImportStatusNew
			createdModels[m.ID] = true
			plan.CreateModels = append(plan.CreateModels, m)
		default:
			c.Status = ImportStatusBlocked
			c.RelatedID = m.RuntimeID
			if bundleRuntimes[m.RuntimeID] == ImportStatusBlocked {
				c.Reason = ImportBlockedDependency
			} else {
				c.Reason = ImportBlockedRuntimeRef
			}
		}
		bundleModels[m.ID] = c.Status
		classify(c)
	}

	// ─── Pipelines ───

	modelResolves := func(id string) bool {
		return localModels[id] != nil || createdModels[id]
	}
	pipelineDependencyBlock := func(p *PipelineEntry) (string, string, bool) {
		for _, e := range p.Models {
			if modelResolves(e.ModelID) {
				continue
			}
			if bundleModels[e.ModelID] == ImportStatusBlocked {
				return ImportBlockedDependency, e.ModelID, true
			}
			return ImportBlockedModelRef, e.ModelID, true
		}
		return "", "", false
	}
	for _, p := range pipelines {
		c := ImportEntityClassification{Type: "pipeline", ID: p.ID, Name: p.Name}
		local := localPipelines[p.ID]
		switch {
		case local != nil && pipelineProjectionEqual(p, local):
			c.Status = ImportStatusUnchanged
			c.RelatedID = p.ID
		case local != nil:
			c.RelatedID = p.ID
			// A Pipeline is only blocked by an empty entry list when a write would
			// leave it empty (ADR 018 D5/D6).
			if len(p.Models) == 0 {
				c.Status = ImportStatusBlocked
				c.Reason = ImportBlockedPipelineEntriesEmpty
			} else if reason, relatedID, blocked := pipelineDependencyBlock(p); blocked {
				c.Status = ImportStatusBlocked
				c.Reason = reason
				c.RelatedID = relatedID
			} else {
				c.Status = ImportStatusUpdate
				plan.UpdatePipelines = append(plan.UpdatePipelines, restoredPipeline(local, p))
			}
		case len(p.Models) == 0:
			c.Status = ImportStatusBlocked
			c.Reason = ImportBlockedPipelineEntriesEmpty
		default:
			if reason, relatedID, blocked := pipelineDependencyBlock(p); blocked {
				c.Status = ImportStatusBlocked
				c.Reason = reason
				c.RelatedID = relatedID
			} else {
				c.Status = ImportStatusNew
				plan.CreatePipelines = append(plan.CreatePipelines, p)
			}
		}
		classify(c)
	}

	return plan
}

// smallestOther returns the lexicographically smallest holder ID that is not id —
// the reported counterpart of a contested name (ADR 018 D-14). It never depends
// on bundle order.
func smallestOther(holders map[string]bool, id string) string {
	ids := make([]string, 0, len(holders))
	for h := range holders {
		if h != id {
			ids = append(ids, h)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
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

func (p *ImportGraphPlan) decrement(dst *ImportCounts, entityType string) {
	switch entityType {
	case "runtime":
		dst.Runtimes--
	case "model":
		dst.Models--
	case "pipeline":
		dst.Pipelines--
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
