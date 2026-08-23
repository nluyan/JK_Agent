//go:build !windows

package agent

import "errors"

// CollectHardwareInfo is only available on Windows, where the WMI provider
// exposes the hardware inventory classes used by the agent.
func CollectHardwareInfo() (string, error) {
	return "", errors.New("hardware collection is only supported on Windows")
}
