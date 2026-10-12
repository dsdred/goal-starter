package storage

// Restorable projections (ADR 018 D11).
//
// Equality of these projections decides UNCHANGED from UPDATE. Every field is
// named explicitly on purpose: a struct-wide comparison would silently pull a
// future field into the restore contract, and ADR 018 D11 forbids that.
//
// Never compared and never restored: Environment values and environment_keys
// (D7 — bundle v1 carries keys only), CreatedAt and UpdatedAt (every legitimate
// restore re-stamps UpdatedAt), and Pipeline entry IDs (D10 — they are
// operational identity referenced by launch instances).
//
// The only normalization permitted is the one D11 lists: a nil Args is the empty
// sequence, an absent WorkingDirectory is "", and an omitted AutostartDelay is 0.
// No trimming, case folding or sorting is applied anywhere.

// runtimeProjectionEqual compares Name, Executable and WorkingDirectory.
func runtimeProjectionEqual(bundle, local *RuntimeEntry) bool {
	return bundle.Name == local.Name &&
		bundle.Executable == local.Executable &&
		bundle.WorkingDirectory == local.WorkingDirectory
}

// modelProjectionEqual compares Name, RuntimeID, ordered Args, Active and
// AutostartDelay.
func modelProjectionEqual(bundle, local *ModelEntry) bool {
	return bundle.Name == local.Name &&
		bundle.RuntimeID == local.RuntimeID &&
		argsEqual(bundle.Args, local.Args) &&
		bundle.Active == local.Active &&
		bundle.AutostartDelay == local.AutostartDelay
}

// pipelineProjectionEqual compares Name, Active and the ordered entry list by
// content only — entry IDs are excluded, so identical content carried under
// different entry IDs is UNCHANGED.
func pipelineProjectionEqual(bundle, local *PipelineEntry) bool {
	if bundle.Name != local.Name || bundle.Active != local.Active {
		return false
	}
	if len(bundle.Models) != len(local.Models) {
		return false
	}
	for i := range bundle.Models {
		b, l := bundle.Models[i], local.Models[i]
		if b.ModelID != l.ModelID || !argsEqual(b.Args, l.Args) || b.AutoStart != l.AutoStart {
			return false
		}
	}
	return true
}

// argsEqual compares element-wise and order-significantly. len(nil) == 0, so a
// nil slice and an empty slice are equivalent; neither operand is mutated.
func argsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Restore constructors (ADR 018 D3/D7/D10/D11).
//
// Each one copies the local record and writes only the restorable projection onto
// the copy. Two rules shape that shape:
//
//   - The bundle entry is never used as the stored record. Bundle v1 materializes
//     Environment as nil (internal/application/portable convertBundle), so storing
//     it would clear the local secret values D7 requires restore to preserve.
//   - The stored record is never mutated in place. Repository.ImportGraph rolls a
//     failed apply back by restoring the previous record pointers, which only
//     works while those pointers still reference the old values (ADR 018 D13).
//
// UpdatedAt is deliberately not touched here: the write path stamps it, like the
// per-entity Update* methods do.

func restoredRuntime(local, bundle *RuntimeEntry) *RuntimeEntry {
	out := *local
	out.Name = bundle.Name
	out.Executable = bundle.Executable
	out.WorkingDirectory = bundle.WorkingDirectory
	return &out
}

func restoredModel(local, bundle *ModelEntry) *ModelEntry {
	out := *local
	out.Name = bundle.Name
	out.RuntimeID = bundle.RuntimeID
	out.Args = cloneArgs(bundle.Args)
	out.Active = bundle.Active
	out.AutostartDelay = bundle.AutostartDelay
	return &out
}

// restoredPipeline restores the pipeline and its entry list per ADR 018 D10.
//
// Entry identity is the entry ID, so matching is by ID, never by position: the
// result walks bundle order and keeps each bundle entry's ID — an ID that matches
// a local entry is that same identity, and an ID that matches nothing is a new
// identity preserving its exported ID. Local entries the bundle does not carry
// are dropped, which is the structural half of restoring membership. An empty
// bundle entry ID stays empty for the write path to fill with the repository's
// ID generator, exactly as CreatePipeline and UpdatePipeline already do; bundle
// validation rejects it with 400 first, so that branch is defensive.
func restoredPipeline(local, bundle *PipelineEntry) *PipelineEntry {
	out := *local
	out.Name = bundle.Name
	out.Active = bundle.Active
	entries := make([]PipelineModel, len(bundle.Models))
	for i, e := range bundle.Models {
		entries[i] = PipelineModel{
			ID:        e.ID,
			ModelID:   e.ModelID,
			Args:      cloneArgs(e.Args),
			AutoStart: e.AutoStart,
		}
	}
	out.Models = entries
	return &out
}

// cloneArgs copies the sequence so a stored record never aliases the bundle's
// backing array. A nil stays nil: D11 normalizes nil and empty as equal for
// comparison, and the write path keeps the stored shape it was given.
func cloneArgs(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
