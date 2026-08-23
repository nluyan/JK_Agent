package agent

import "strings"

// HardwareInfoCommand is the reserved script value used by the existing
// ExecutePowershellScript channel to request native hardware collection.
const HardwareInfoCommand = "__JK_AGENT_COLLECT_HARDWARE_INFO__"

func isHardwareInfoCommand(script string) bool {
	return strings.TrimSpace(script) == HardwareInfoCommand
}
