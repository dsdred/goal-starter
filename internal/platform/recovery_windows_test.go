//go:build windows

package platform

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestFiletimeNanoseconds_KnownValue(t *testing.T) {
	// Filetime.Nanoseconds() returns nanoseconds since Unix epoch (1970-01-01).
	// For 2000-01-01 00:00:00 UTC: 946684800 seconds = 946684800 * 1e9 nanos
	expected := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	expectedNanos := expected.UnixNano()

	// Build a FILETIME for 2000-01-01 00:00:00 UTC.
	// FILETIME is 100-ns intervals since 1601-01-01.
	// Unix epoch in 100ns intervals: 116444736000000000
	// 2000-01-01 in 100ns intervals: 116444736000000000 + 946684800*10000000
	const unixEpoch100ns = 116444736000000000
	const secondsTo2000 = 946684800
	raw := unixEpoch100ns + secondsTo2000*10000000

	ft := windows.Filetime{
		LowDateTime:  uint32(raw & 0xFFFFFFFF),
		HighDateTime: uint32(raw >> 32),
	}

	got := ft.Nanoseconds()
	if got != expectedNanos {
		t.Errorf("Nanoseconds(): expected %d (%v), got %d", expectedNanos, expected, got)
	}
}

func TestFiletimeNanoseconds_UnixEpoch(t *testing.T) {
	// Unix epoch (1970-01-01) should return 0.
	const unixEpoch100ns = 116444736000000000
	ft := windows.Filetime{
		LowDateTime:  uint32(unixEpoch100ns & 0xFFFFFFFF),
		HighDateTime: uint32(unixEpoch100ns >> 32),
	}

	got := ft.Nanoseconds()
	if got != 0 {
		t.Errorf("Unix epoch: expected 0, got %d", got)
	}
}

func TestFiletimeToTimeConversion(t *testing.T) {
	// Verify the conversion path used in GetProcessIdentity:
	// nanos = creation.Nanoseconds() → time.Unix(nanos/1e9, nanos%1e9)
	const unixEpoch100ns = 116444736000000000
	const secondsTo2000 = 946684800
	raw := unixEpoch100ns + secondsTo2000*10000000

	ft := windows.Filetime{
		LowDateTime:  uint32(raw & 0xFFFFFFFF),
		HighDateTime: uint32(raw >> 32),
	}

	nanos := ft.Nanoseconds()
	result := time.Unix(nanos/1e9, nanos%1e9)
	expected := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	if !result.Equal(expected) {
		t.Errorf("conversion: expected %v, got %v", expected, result)
	}
}
