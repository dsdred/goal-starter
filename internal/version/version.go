package version

import "fmt"

// Version, GitCommit, and BuildTime are set at build time via -ldflags.
var (
	Version   = "dev"
	GitCommit = "none"
	BuildTime = "unknown"
)

// Info returns a formatted version string for --version flag and status endpoint.
func Info() string {
	return fmt.Sprintf("GoAl %s (commit: %s, time: %s)", Version, GitCommit, BuildTime)
}
