//go:build !windows

package agent

import (
	"fmt"
	"time"
)

// runWallpaperHelper 在非 Windows 平台不可用。
func runWallpaperHelper(session UserSession, timeout time.Duration, args ...string) (string, error) {
	return "", errUserSessionUnsupported
}

// RunWallpaperChild 在非 Windows 平台直接返回失败。
func RunWallpaperChild(args []string) int {
	fmt.Println(exitUnsupported("壁纸功能"))
	return 1
}
