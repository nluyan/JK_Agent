package agent

import "testing"

func TestIsHardwareInfoCommand(t *testing.T) {
	if !isHardwareInfoCommand("  " + HardwareInfoCommand + "\r\n") {
		t.Fatal("hardware command should allow surrounding whitespace")
	}
	if isHardwareInfoCommand(HardwareInfoCommand + " extra") {
		t.Fatal("hardware command must be an exact reserved value")
	}
}
