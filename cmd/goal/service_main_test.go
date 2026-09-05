package main

import (
	"testing"
)

// TestServiceMainUnknownVerb verifies the bounded refusal for a bogus verb on
// every platform (ADR 011 D1.1).
func TestServiceMainUnknownVerb(t *testing.T) {
	if code := serviceMain("bogus", "GoAl", "auto", t.TempDir()+"/goal.json"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestServiceMainRunOutsideSCM verifies that --service run outside an SCM
// session fails with a bounded error and a non-zero exit (ADR 011 D1.2,
// acceptance item 2).
func TestServiceMainRunOutsideSCM(t *testing.T) {
	dir := t.TempDir()
	if code := serviceMain("run", "GoAl", "auto", dir+"/goal.json"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestServiceMainStartRefusedWithoutRegistration verifies that `start` of an
// unregistered service is refused before any SCM operation succeeds.
func TestServiceMainStartRefusedWithoutRegistration(t *testing.T) {
	if code := serviceMain("start", "definitely-not-a-real-goal-service", "auto", t.TempDir()+"/goal.json"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestServiceMainStatusRefusedWithoutRegistration verifies the bounded
// "not found" diagnostic for status (ADR 011 D9).
func TestServiceMainStatusRefusedWithoutRegistration(t *testing.T) {
	if code := serviceMain("status", "definitely-not-a-real-goal-service", "auto", t.TempDir()+"/goal.json"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestServiceMainUninstallRefusedWithoutRegistration verifies the bounded
// "not found" diagnostic for uninstall (ADR 011 D9).
func TestServiceMainUninstallRefusedWithoutRegistration(t *testing.T) {
	if code := serviceMain("uninstall", "definitely-not-a-real-goal-service", "auto", t.TempDir()+"/goal.json"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}
