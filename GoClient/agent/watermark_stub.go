//go:build !windows

package agent

import "fmt"

// startWatermarkRenderer 在非 Windows 平台不可用。
func startWatermarkRenderer(policy watermarkPolicy, session UserSession) (int, error) {
	return 0, errUserSessionUnsupported
}

// stopWatermarkRenderer 在非 Windows 平台不可用。
func stopWatermarkRenderer() (bool, error) {
	return false, errUserSessionUnsupported
}

// watermarkRendererStatus 在非 Windows 平台恒为未运行。
func watermarkRendererStatus() (int, uint32, bool) {
	return 0, 0, false
}

// RunWatermarkChild 在非 Windows 平台直接返回失败。
func RunWatermarkChild(args []string) int {
	fmt.Println(exitUnsupported("水印功能"))
	return 1
}
