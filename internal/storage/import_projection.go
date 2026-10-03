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
