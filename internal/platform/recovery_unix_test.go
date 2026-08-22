//go:build !windows

package platform

import (
	"testing"
	"time"
)

func TestParseProcStatStartTime_SimpleComm(t *testing.T) {
	// pid (comm) state ... starttime at field 22
	// After ")": state ppid pgrp session tty_nr tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime priority nice num_threads itrealvalue starttime
	// Index 19 after ")" = starttime
	data := []byte("1234 (myapp) S 1 1234 1234 0 -1 4194304 100 0 0 0 10 20 0 0 20 0 1 0 99999999")
	result, ok := parseProcStatStartTime(data)
	if !ok {
		t.Fatal("expected successful parse")
	}
	if result.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestParseProcStatStartTime_CommWithSpaces(t *testing.T) {
	// comm contains spaces
	data := []byte("5678 (my app with spaces) S 1 5678 5678 0 -1 4194304 100 0 0 0 10 20 0 0 20 0 1 0 88888888")
	result, ok := parseProcStatStartTime(data)
	if !ok {
		t.Fatal("expected successful parse with spaces in comm")
	}
	if result.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestParseProcStatStartTime_CommWithParentheses(t *testing.T) {
	// comm contains parentheses (tricky case)
	data := []byte("9999 (proc (test)) S 1 9999 9999 0 -1 4194304 100 0 0 0 10 20 0 0 20 0 1 0 77777777")
	result, ok := parseProcStatStartTime(data)
	if !ok {
		t.Fatal("expected successful parse with parens in comm")
	}
	if result.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestParseProcStatStartTime_InvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"no closing paren", []byte("1234 (comm S 1 1234")},
		{"too few fields", []byte("1234 (comm) S 1 1234")},
		{"empty", []byte("")},
		{"non-numeric starttime", []byte("1234 (comm) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 0 0 abc")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseProcStatStartTime(tt.data)
			if ok {
				t.Error("expected parse failure")
			}
		})
	}
}

func TestParseProcStatStartTime_FieldCounting(t *testing.T) {
	// Verify that field 22 (starttime) is correctly identified as index 19 after ")"
	// Build a known stat line with starttime = 100
	// Fields after ")": state(0) ppid(1) pgrp(2) session(3) tty_nr(4) tpgid(5)
	// flags(6) minflt(7) cminflt(8) majflt(9) cmajflt(10) utime(11) stime(12)
	// cutime(13) cstime(14) priority(15) nice(16) num_threads(17) itrealvalue(18) starttime(19)
	data := []byte("1 (X) S 0 1 1 0 -1 0 1 2 3 4 5 6 7 8 9 10 11 12 13 100")
	result, ok := parseProcStatStartTime(data)
	if !ok {
		t.Fatal("expected successful parse")
	}
	// starttime=100 ticks, CLK_TCK=100 → 1 second after boot
	// Boot time + 1 second
	expected := getBootTimeOrZero(t).Add(1 * time.Second)
	if result.Sub(expected) > time.Second || expected.Sub(result) > time.Second {
		t.Errorf("starttime conversion: expected ~%v, got %v", expected, result)
	}
}

func getBootTimeOrZero(t *testing.T) time.Time {
	t.Helper()
	bt, err := getBootTime()
	if err != nil {
		t.Skipf("cannot read /proc/stat: %v", err)
	}
	return bt
}
