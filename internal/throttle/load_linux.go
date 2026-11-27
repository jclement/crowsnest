//go:build linux

package throttle

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// HostLoad reads the one-minute load average from /proc/loadavg.
func HostLoad() (float64, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, errors.New("/proc/loadavg is empty")
	}
	return strconv.ParseFloat(fields[0], 64)
}
