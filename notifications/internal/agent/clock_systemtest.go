//go:build systemtest

package agent

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ClockNow is replaced only in explicit system-test builds. Missing or invalid
// configuration fails closed instead of accidentally using a real endpoint/time.
func ClockNow() time.Time {
	data, err := os.ReadFile(os.Getenv("NOVASCALE_SYSTEMTEST_CLOCK"))
	if err != nil {
		panic("system-test clock unavailable")
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		panic("invalid system-test clock")
	}
	return time.Unix(seconds, 0).UTC()
}
