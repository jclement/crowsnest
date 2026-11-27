//go:build darwin

package throttle

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// HostLoad reads the one-minute load average from the kernel (sysctl vm.loadavg: three fixed-
// point numbers and their scale).
func HostLoad() (float64, error) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(raw) < 24 {
		return 0, errors.New("vm.loadavg is shorter than expected")
	}
	load1 := binary.LittleEndian.Uint32(raw[0:4])
	scale := binary.LittleEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0, errors.New("vm.loadavg has no scale")
	}
	return float64(load1) / float64(scale), nil
}
