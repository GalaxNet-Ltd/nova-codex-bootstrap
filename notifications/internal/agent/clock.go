//go:build !systemtest

package agent

import "time"

// ClockNow keeps event and enrollment time consistent with the daemon clock.
func ClockNow() time.Time { return time.Now() }
