package resource

import (
	"context"
	"time"
)

// Aggregator combines the reports of its providers into one ResourceReport
// (ADR 026 D-7). It is read-only: it samples each provider once per Report call,
// concatenates devices without summing across memory kinds (D-4), and derives
// the report state only from provider outcomes. It holds no cache and no
// background activity (D-3).
type Aggregator struct {
	providers []ResourceProvider
}

// NewAggregator builds an aggregator over the given providers. With zero
// providers the report is unavailable (D-7/D-8).
func NewAggregator(providers ...ResourceProvider) *Aggregator {
	return &Aggregator{providers: providers}
}

// Report samples every provider once and derives the universal report.
func (a *Aggregator) Report(ctx context.Context) ResourceReport {
	report := ResourceReport{
		Devices:      []ResourceDevice{},
		Processes:    []ProcessReading{},
		Capabilities: map[string]ProviderCapabilities{},
		Errors:       []ProviderError{},
	}

	okCount, failedCount := 0, 0
	for _, p := range a.providers {
		pr := p.Sample(ctx)
		report.Devices = append(report.Devices, pr.Devices...)
		report.Processes = append(report.Processes, pr.Processes...)
		if pr.System != nil {
			// Only the system provider populates System (RES-03B); there is
			// exactly one such provider, so no cross-provider merge exists.
			report.System = pr.System
		}
		report.Capabilities[pr.ProviderID] = pr.Capabilities
		switch pr.State {
		case StateOK:
			okCount++
		case StateMeasurementFailed:
			failedCount++
			report.Errors = append(report.Errors, ProviderError{Provider: pr.ProviderID, State: pr.State, Reason: pr.Error})
		default:
			// unavailable: no source present (healthy CPU-only/headless, D-8).
			report.Errors = append(report.Errors, ProviderError{Provider: pr.ProviderID, State: pr.State, Reason: pr.Error})
		}
	}

	report.State = deriveState(okCount, failedCount)
	if report.State == StateOK || report.State == StatePartial {
		report.SampledAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return report
}

// deriveState applies the D-7 rules in their sealed order. Optional-metric
// absence never enters here: it is a Reading provenance, not a provider outcome.
func deriveState(okCount, failedCount int) State {
	switch {
	case okCount == 0 && failedCount == 0:
		return StateUnavailable
	case failedCount > 0 && okCount > 0:
		return StatePartial
	case failedCount > 0:
		return StateMeasurementFailed
	default:
		return StateOK
	}
}
