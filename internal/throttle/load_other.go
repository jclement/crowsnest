//go:build !darwin && !linux

package throttle

import "errors"

// HostLoad is not available on this OS; the load hold stays off.
func HostLoad() (float64, error) { return 0, errors.New("the load average is not read on this system") }
