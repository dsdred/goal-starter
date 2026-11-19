package platform

import "time"

// NewNvidiaSmiProviderWithLookup returns an NvidiaSmiProvider with an injected
// binary-discovery function and timeout. It is the test seam (ADR 025 D-10) used
// to exercise the real exec.CommandContext + CSV-parse + kill path with a fake
// executable from outside this package; production uses NewNvidiaSmiProvider.
// It adds no behavior: the returned provider is identical to the production one
// apart from the injected discovery and deadline.
func NewNvidiaSmiProviderWithLookup(timeout time.Duration, lookup func() (string, error)) *NvidiaSmiProvider {
	return &NvidiaSmiProvider{timeout: timeout, lookup: lookup}
}
