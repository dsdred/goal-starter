// Command proc-identity probes one PID for the process-identity anchors that
// production recovery uses, and prints a single JSON line.
//
// It exists only for the maintained browser acceptance suite: a browser leg has
// to prove that the managed process was still alive as *this run's* executable
// (ADR 021 F-7.2), and re-implementing /proc or Win32 probing in Node would
// measure something other than the anchors verifyIdentity compares. The probe
// therefore calls the same platform functions the supervisor calls.
//
// Test-only: like testdata/fake-runtime and testdata/tls-fixture it is built
// explicitly by the harness and is never part of the shipped binary.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/dsdred/goal/internal/platform"
)

type result struct {
	PID            int    `json:"pid"`
	Alive          bool   `json:"alive"`
	ExecutablePath string `json:"executable_path,omitempty"`
	StartTime      string `json:"start_time,omitempty"`
	HasStartTime   bool   `json:"has_start_time"`
	AliveError     string `json:"alive_error,omitempty"`
	IdentityError  string `json:"identity_error,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: proc-identity <pid>")
		os.Exit(2)
	}
	pid, err := strconv.Atoi(os.Args[1])
	if err != nil || pid <= 0 {
		fmt.Fprintf(os.Stderr, "invalid pid %q\n", os.Args[1])
		os.Exit(2)
	}

	prober := platform.NewRecoveryProber()
	res := result{PID: pid}
	alive, aliveErr := prober.IsProcessAlive(pid)
	res.Alive = alive
	if aliveErr != nil {
		res.AliveError = aliveErr.Error()
	}
	if alive {
		identity, identityErr := prober.GetProcessIdentity(pid)
		if identityErr != nil {
			res.IdentityError = identityErr.Error()
		} else {
			res.ExecutablePath = identity.ExecutablePath
			res.HasStartTime = identity.HasStartTime
			if identity.HasStartTime {
				res.StartTime = identity.StartTime.UTC().Format(time.RFC3339Nano)
			}
		}
	}

	// A dead or unresolvable PID is a measurement outcome, not a probe failure,
	// so it still exits 0 with alive=false. Only misuse or an un-writable stdout
	// exits non-zero, which keeps "the probe broke" distinguishable from
	// "the process is gone".
	if err := json.NewEncoder(os.Stdout).Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
