// Package resource is the vendor-neutral resource observation model (ADR 026).
// It is pure Go and cross-platform: it makes no OS calls itself. Concrete
// vendor×OS providers live in internal/platform; this package imports platform
// for the provider seam, never the reverse (D-2). Monitoring is read-only and
// separate from Lifecycle and Scheduling (D-1).
package resource

import "context"

// Vendor identifies the hardware/source vendor (D-3).
type Vendor string

const (
	VendorNVIDIA Vendor = "nvidia"
	VendorAMD    Vendor = "amd"
	VendorIntel  Vendor = "intel"
	VendorApple  Vendor = "apple"
	VendorSystem Vendor = "system"
)

// ResourceType is an extensible metric enum (D-5). New types are additive and
// never break the slice-1 contract.
type ResourceType string

const (
	ResourceVRAMTotal ResourceType = "vram_total"
	ResourceVRAMUsed  ResourceType = "vram_used"
	ResourceVRAMFree  ResourceType = "vram_free"
)

// Unit is the explicit unit of a Reading (D-5). Memory is bytes; utilization is
// percent.
type Unit string

const (
	UnitBytes   Unit = "bytes"
	UnitPercent Unit = "percent"
)

// Provenance distinguishes a measured counter from an estimate from absence
// (D-6). A measured zero IS a value; unavailable is nil, never 0 or -1.
type Provenance string

const (
	ProvenanceMeasured    Provenance = "measured"
	ProvenanceEstimated   Provenance = "estimated"
	ProvenanceUnavailable Provenance = "unavailable"
)

// MemoryClass is the kind of memory a device reports (D-4). A device reports
// either dedicated (+ optional shared) or unified, never both; the aggregator
// never sums across kinds.
type MemoryClass string

const (
	MemoryDedicated MemoryClass = "dedicated"
	MemoryShared    MemoryClass = "shared"
	MemoryUnified   MemoryClass = "unified"
)

// State is a report/provider outcome (D-7). Provider-level outcomes are ok /
// unavailable / measurement_failed; partial is an aggregate-only state reachable
// with ≥2 providers. unavailable (no source) ≠ measurement_failed (source broke).
type State string

const (
	StateOK                State = "ok"
	StatePartial           State = "partial"
	StateUnavailable       State = "unavailable"
	StateMeasurementFailed State = "measurement_failed"
)

// Reading is one metric value with explicit provenance (D-6). Value is nil
// (JSON null) when unavailable — never a fabricated 0 or -1.
type Reading struct {
	Value      *int64     `json:"value"`
	Unit       Unit       `json:"unit"`
	Provenance Provenance `json:"provenance"`
	Source     string     `json:"source"`
	Reason     string     `json:"reason,omitempty"`
}

// MeasuredReading builds a measured Reading from a real counter value (a
// measured zero is a value, D-6).
func MeasuredReading(value int64, unit Unit, source string) Reading {
	v := value
	return Reading{Value: &v, Unit: unit, Provenance: ProvenanceMeasured, Source: source}
}

// UnavailableReading builds an unavailable Reading: Value nil, bounded reason.
func UnavailableReading(unit Unit, source, reason string) Reading {
	return Reading{Value: nil, Unit: unit, Provenance: ProvenanceUnavailable, Source: source, Reason: reason}
}

// ResourceDevice is one enumerated device with a stable vendor-namespaced
// DeviceID (identity) and a current Index (position) (D-9).
type ResourceDevice struct {
	DeviceID    string                   `json:"device_id"`
	Index       int                      `json:"index"`
	Vendor      Vendor                   `json:"vendor"`
	Name        string                   `json:"name"`
	Driver      string                   `json:"driver"`
	MemoryClass MemoryClass              `json:"memory_class"`
	Resources   map[ResourceType]Reading `json:"resources"`
}

// CapabilityLevel is the three distinct capability levels for one metric (D-3):
// supported (static design fact), available (source addressable this request),
// sampled (value obtained this sample). They are never conflated.
type CapabilityLevel struct {
	Supported bool `json:"supported"`
	Available bool `json:"available"`
	Sampled   bool `json:"sampled"`
}

// ProviderCapabilities is the per-metric three-level capability matrix of one
// provider, derived within the current request from its single sample (D-3).
type ProviderCapabilities struct {
	Vendor  Vendor                           `json:"vendor"`
	Metrics map[ResourceType]CapabilityLevel `json:"metrics"`
}

// Capabilities is the provider's static supported-metric declaration (D-3
// supported level). It changes only when the provider implementation changes.
type Capabilities struct {
	Supported map[ResourceType]bool
}

// ProviderError is a bounded error entry for a provider that did not succeed
// (D-11): provider id, outcome state, bounded reason token — never raw text.
type ProviderError struct {
	Provider string `json:"provider"`
	State    State  `json:"state"`
	Reason   string `json:"reason"`
}

// ProviderReport is the outcome of one provider's single bounded sample.
type ProviderReport struct {
	ProviderID   string
	Vendor       Vendor
	State        State
	Devices      []ResourceDevice
	Capabilities ProviderCapabilities
	Error        string
	SampledAt    string
}

// SystemResources reserves the future system RAM/CPU observation (D-8). Slice 1
// has no system provider, so ResourceReport.System is nil (JSON null).
type SystemResources struct{}

// ProcessReading reserves future per-process attribution (D-10). Slice 1
// populates nothing; ResourceReport.Processes is an empty array.
type ProcessReading struct{}

// ResourceReport is the universal API body (D-11). Devices and Processes are
// empty arrays never null; System is null in slice 1; SampledAt is present only
// when state ok/partial (D-7).
type ResourceReport struct {
	State        State                           `json:"state"`
	Devices      []ResourceDevice                `json:"devices"`
	System       *SystemResources                `json:"system"`
	Processes    []ProcessReading                `json:"processes"`
	Capabilities map[string]ProviderCapabilities `json:"capabilities"`
	SampledAt    string                          `json:"sampled_at,omitempty"`
	Errors       []ProviderError                 `json:"errors"`
}

// ResourceProvider is the seam the aggregator consumes (D-3). Implementations
// wrap a concrete platform provider; the HTTP handler never manages exec.Cmd.
type ResourceProvider interface {
	ID() string
	Vendor() Vendor
	Capabilities() Capabilities
	Sample(ctx context.Context) ProviderReport
}
