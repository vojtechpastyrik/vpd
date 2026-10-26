package gitlab

import "context"

// Host is the GitLab server the upgrade runs on.
type Host interface {
	// Run executes a bash script as root on the host and returns its stdout.
	Run(ctx context.Context, script string) (string, error)
}
