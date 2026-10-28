//go:build !windows

package agent

import (
	"time"
)

// TokenMode 决定投递到用户会话的进程使用哪种令牌。
type TokenMode int

const (
	// TokenAsLoggedOnUser 使用登录用户自身的令牌。
	TokenAsLoggedOnUser TokenMode = iota
	// TokenAsSystem 使用服务自身的 SYSTEM 令牌但显示在用户桌面。
	TokenAsSystem
)

// UserSession 描述一个已登录的交互会话。
type UserSession struct {
	SessionID uint32
	Domain    string
	User      string
}

// HasUser 判断该会话是否真的有用户登录。
func (s UserSession) HasUser() bool { return false }

// AccountName 返回账户名。
func (s UserSession) AccountName() string { return "" }

// DisplayName 返回可读的会话描述。
func (s UserSession) DisplayName() string { return "会话0" }

// UserSessionProcess 是投递到用户会话中的子进程句柄。
type UserSessionProcess struct {
	PID uint32
}

// Close 释放句柄。
func (p *UserSessionProcess) Close() {}

// Wait 等待子进程退出。
func (p *UserSessionProcess) Wait(timeout time.Duration) bool { return true }

// Terminate 结束子进程。
func (p *UserSessionProcess) Terminate() {}

// userSessionSupported 报告当前平台是否支持用户会话投递。
func userSessionSupported() bool { return false }

// ActiveConsoleSession 返回物理控制台所在的交互会话。
func ActiveConsoleSession() (UserSession, error) { return UserSession{}, errUserSessionUnsupported }

// ListUserSessions 枚举所有活动状态的交互会话。
func ListUserSessions() ([]UserSession, error) { return nil, errUserSessionUnsupported }

// ResolveTargetSession 解析目标会话。
func ResolveTargetSession(sessionID uint32, explicit bool) (UserSession, error) {
	return UserSession{}, errUserSessionUnsupported
}

// LaunchInUserSession 在指定会话的交互式桌面上启动进程。
func LaunchInUserSession(session UserSession, mode TokenMode, exePath string, args []string, workDir string) (*UserSessionProcess, error) {
	return nil, errUserSessionUnsupported
}

// JikeAgentDataDir 返回 agent 的共享数据目录。
func JikeAgentDataDir() (string, error) { return "", errUserSessionUnsupported }

// AssetsDir 返回只读资源目录。
func AssetsDir() (string, error) { return "", errUserSessionUnsupported }

// StateDir 返回 agent 状态目录。
func StateDir() (string, error) { return "", errUserSessionUnsupported }

// SystemTempDir 返回 SYSTEM 令牌模式专用的临时目录。
func SystemTempDir() (string, error) { return "", errUserSessionUnsupported }

// UserShareDir 返回用户令牌模式专用的临时目录。
func UserShareDir() (string, error) { return "", errUserSessionUnsupported }

// ResolvePowerShellPath 返回 PowerShell 可执行文件的绝对路径。
func ResolvePowerShellPath() string { return "" }

// CurrentExecutablePath 返回 agent 自身的绝对路径。
func CurrentExecutablePath() (string, error) { return "", errUserSessionUnsupported }
