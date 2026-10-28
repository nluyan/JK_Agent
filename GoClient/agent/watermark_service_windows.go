//go:build windows

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// processSynchronizeAccess 仅用于探测进程是否存活，是权限最小的打开方式。
	processSynchronizeAccess = 0x00100000
	// processTerminateAccess 用于结束失控的水印进程。
	processTerminateAccess = 0x0001
	// processQueryLimitedInformation 用于读取进程映像路径，确认确实是自己的水印进程。
	processQueryLimitedInformation = 0x1000
)

var procQueryFullProcessImageNameW = modKernel32Mutex.NewProc("QueryFullProcessImageNameW")

// watermarkPIDRecord 是渲染进程写下的 PID 记录，服务重启后据此接管或清理。
type watermarkPIDRecord struct {
	PID       int    `json:"pid"`
	SessionID uint32 `json:"sessionId"`
	StartedAt string `json:"startedAt,omitempty"`
}

// startWatermarkRenderer 以 SYSTEM 令牌把水印渲染进程投递到登录用户的交互式桌面。
// 必须用 SYSTEM 而不是用户令牌：进程属主是 SYSTEM 时，普通用户在任务管理器里
// 结束它会得到"拒绝访问"，这是水印"关不掉"的关键。
//
// 渲染进程是常驻的，这里只等它建好窗口并回写结果，不等待其退出。
func startWatermarkRenderer(policy watermarkPolicy, session UserSession) (int, error) {
	exePath, err := CurrentExecutablePath()
	if err != nil {
		return 0, err
	}
	pidFilePath, err := watermarkPIDFilePath()
	if err != nil {
		return 0, err
	}
	// 结果文件放在 SYSTEM 专用目录：本进程以 SYSTEM 身份运行，
	// 绝不能读写 Users 可写的 tmp-user，否则构成提权路径。
	systemDir, err := SystemTempDir()
	if err != nil {
		return 0, err
	}
	resultPath := filepath.Join(systemDir, fmt.Sprintf("watermark_%d.result.txt", time.Now().UnixNano()))
	defer os.Remove(resultPath)

	args := []string{
		"--watermark",
		"-text", policy.Text,
		"-opacity", strconv.Itoa(policy.Opacity),
		"-color", policy.Color,
		"-fontSize", strconv.Itoa(policy.FontSize),
		"-angle", strconv.Itoa(policy.Angle),
		"-spacingX", strconv.Itoa(policy.SpacingX),
		"-spacingY", strconv.Itoa(policy.SpacingY),
		"-session", strconv.FormatUint(uint64(session.SessionID), 10),
		"-pidfile", pidFilePath,
		"-" + childResultFlag, resultPath,
	}

	process, err := LaunchInUserSession(session, TokenAsSystem, exePath, args, systemDir)
	if err != nil {
		return 0, err
	}
	defer process.Close()

	process.Wait(watermarkRendererStartupGrace)

	if !processAlive(process.PID) {
		output := strings.TrimSpace(decodeWithBomAndFallback(readFileIfExists(resultPath)))
		if output != "" {
			return 0, fmt.Errorf("水印进程启动后立即退出: %s", output)
		}
		return 0, errors.New("水印进程启动后立即退出")
	}
	return int(process.PID), nil
}

// stopWatermarkRenderer 结束水印渲染进程并清理 PID 记录。
// 结束前会校验映像路径确实是本 agent，避免 PID 复用误杀无关进程。
func stopWatermarkRenderer() (bool, error) {
	pidPath, err := watermarkPIDFilePath()
	if err != nil {
		return false, err
	}
	stopped := false
	if data, readErr := os.ReadFile(pidPath); readErr == nil {
		record := watermarkPIDRecord{}
		if json.Unmarshal(data, &record) == nil && record.PID > 0 {
			pid := uint32(record.PID)
			if processAlive(pid) && isOwnWatermarkProcess(pid) {
				stopped = terminateProcessByPID(pid)
			}
		}
	}
	_ = os.Remove(pidPath)
	return stopped, nil
}

// watermarkRendererStatus 判断水印进程是否仍在运行。
// 以全局互斥体为准，PID 文件只提供 PID 与会话号：进程被强制结束后
// 内核会自动释放互斥体，而 PID 文件会残留，单看 PID 容易误判。
func watermarkRendererStatus() (int, uint32, bool) {
	mutex := openWatermarkMutex()
	mutexHeld := mutex != 0
	if mutexHeld {
		windows.CloseHandle(mutex)
	}

	pidPath, err := watermarkPIDFilePath()
	if err != nil {
		return 0, 0, false
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, 0, false
	}
	record := watermarkPIDRecord{}
	if json.Unmarshal(data, &record) != nil || record.PID <= 0 {
		return 0, 0, false
	}
	pid := uint32(record.PID)
	if !mutexHeld || !processAlive(pid) || !isOwnWatermarkProcess(pid) {
		return record.PID, record.SessionID, false
	}
	return record.PID, record.SessionID, true
}

// processAlive 探测指定 PID 的进程是否仍在运行。
func processAlive(pid uint32) bool {
	if pid == 0 {
		return false
	}
	handle, err := windows.OpenProcess(processSynchronizeAccess, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}
	// 进程对象在退出时变为 signaled，因此等待立即返回说明进程已经结束。
	return event != waitObjectEvent
}

// isOwnWatermarkProcess 校验 PID 对应的映像就是本 agent，防止 PID 复用时误杀无关进程。
func isOwnWatermarkProcess(pid uint32) bool {
	exePath, err := CurrentExecutablePath()
	if err != nil {
		return false
	}
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))
	ret, _, _ := procQueryFullProcessImageNameW.Call(uintptr(handle), 0,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 || size == 0 {
		return false
	}
	return strings.EqualFold(windows.UTF16ToString(buffer[:size]), exePath)
}

// terminateProcessByPID 结束指定进程，拒绝结束自身。
func terminateProcessByPID(pid uint32) bool {
	if pid == 0 || pid == uint32(os.Getpid()) {
		return false
	}
	handle, err := windows.OpenProcess(processTerminateAccess, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 0) == nil
}
