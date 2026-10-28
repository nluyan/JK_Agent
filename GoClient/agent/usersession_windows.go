//go:build windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// interactiveDesktop 是登录用户所在的交互式桌面。服务进程运行在 Session 0，
	// 必须显式指定该桌面，创建的窗口才会出现在用户屏幕上。
	interactiveDesktop = "WinSta0\\Default"

	createUnicodeEnvironmentFlag = 0x00000400
	createNoWindowFlag           = 0x08000000

	waitObjectEvent = 0x00000000

	// servicesWindowStation 是 Session 0 的窗口站名，枚举交互会话时必须排除。
	servicesWindowStation = "services"

	// wellKnownUsersSID 是内置 Users 组的 SID。用 SID 而不是组名，
	// 避免中英文系统上组名不一致导致授权失败。
	wellKnownUsersSID = "S-1-5-32-545"

	// invalidConsoleSession 是 WTSGetActiveConsoleSessionId 在无控制台会话时的返回值。
	invalidConsoleSession = 0xFFFFFFFF

	jikeAgentDataDirName = "JikeAgent"
)

// WTSQuerySessionInformation 的信息类别。x/sys/windows 未导出该 API，下面用 LazyProc 自建。
const (
	wtsInfoUserName   = 5
	wtsInfoDomainName = 7
)

var (
	modWtsapi32                    = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQuerySessionInformation = modWtsapi32.NewProc("WTSQuerySessionInformationW")

	// grantedUserShare 记录已完成 Users 授权的目录，避免每次调用都改写 ACL。
	grantedUserShare sync.Map
)

// TokenMode 决定投递到用户会话的进程使用哪种令牌。
type TokenMode int

const (
	// TokenAsLoggedOnUser 使用登录用户自身的令牌。进程能正确读写该用户的 HKCU，
	// 壁纸这类"必须以用户身份生效"的操作走这条；代价是用户可以在任务管理器里结束它。
	TokenAsLoggedOnUser TokenMode = iota
	// TokenAsSystem 使用服务自身的 SYSTEM 令牌，但桌面指向用户的 WinSta0\Default。
	// 窗口在用户屏幕上可见，而进程属主是 SYSTEM，普通用户结束进程会得到"拒绝访问"。
	// 水印需要"关不掉"，走这条。
	TokenAsSystem
)

// UserSession 描述一个已登录的交互会话。
type UserSession struct {
	SessionID uint32
	Domain    string
	User      string
}

// HasUser 判断该会话是否真的有用户登录。
func (s UserSession) HasUser() bool {
	return strings.TrimSpace(s.User) != ""
}

// AccountName 返回 DOMAIN\user 形式的账户名，无域时只返回用户名。
func (s UserSession) AccountName() string {
	user := strings.TrimSpace(s.User)
	if user == "" {
		return ""
	}
	domain := strings.TrimSpace(s.Domain)
	if domain == "" {
		return user
	}
	return domain + "\\" + user
}

// DisplayName 用于回传给服务端的可读会话描述。
func (s UserSession) DisplayName() string {
	account := s.AccountName()
	if account == "" {
		return fmt.Sprintf("会话%d", s.SessionID)
	}
	return fmt.Sprintf("会话%d(%s)", s.SessionID, account)
}

// querySessionString 读取指定会话的字符串信息，失败时返回空串。
func querySessionString(sessionID uint32, infoClass uint32) string {
	var buffer *uint16
	var bytesReturned uint32
	ret, _, _ := procWTSQuerySessionInformation.Call(
		uintptr(0), // WTS_CURRENT_SERVER_HANDLE
		uintptr(sessionID),
		uintptr(infoClass),
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(unsafe.Pointer(&bytesReturned)),
	)
	if ret == 0 || buffer == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buffer)))
	return windows.UTF16PtrToString(buffer)
}

// describeSession 补全会话的域名与用户名。
func describeSession(sessionID uint32) UserSession {
	return UserSession{
		SessionID: sessionID,
		Domain:    querySessionString(sessionID, wtsInfoDomainName),
		User:      querySessionString(sessionID, wtsInfoUserName),
	}
}

// ActiveConsoleSession 返回物理控制台所在的交互会话，即用户当前正在使用的那个会话。
// 本客户端只投递到控制台会话，不做多会话广播。
func ActiveConsoleSession() (UserSession, error) {
	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID == invalidConsoleSession || sessionID == 0 {
		return UserSession{}, errNoLoggedOnUser
	}
	session := describeSession(sessionID)
	if !session.HasUser() {
		return UserSession{}, errNoLoggedOnUser
	}
	return session, nil
}

// ListUserSessions 枚举所有活动状态且已登录用户的交互会话，用于诊断与显式指定会话。
func ListUserSessions() ([]UserSession, error) {
	var info *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(windows.Handle(0), 0, 1, &info, &count); err != nil {
		return nil, fmt.Errorf("枚举终端服务会话失败: %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))

	if count == 0 || info == nil {
		return nil, nil
	}

	sessions := make([]UserSession, 0, count)
	for _, item := range unsafe.Slice(info, count) {
		// Session 0 是服务会话，窗口站名为 Services，用户看不到其中的任何窗口。
		if item.SessionID == 0 || item.State != windows.WTSActive {
			continue
		}
		if strings.EqualFold(windows.UTF16PtrToString(item.WindowStationName), servicesWindowStation) {
			continue
		}
		if session := describeSession(item.SessionID); session.HasUser() {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

// ResolveTargetSession 解析目标会话：未显式指定时使用控制台会话。
func ResolveTargetSession(sessionID uint32, explicit bool) (UserSession, error) {
	if !explicit {
		return ActiveConsoleSession()
	}
	if sessionID == 0 || sessionID == invalidConsoleSession {
		return UserSession{}, errors.New("会话ID无效")
	}
	session := describeSession(sessionID)
	if !session.HasUser() {
		return UserSession{}, fmt.Errorf("会话%d当前没有已登录的用户", sessionID)
	}
	return session, nil
}

// UserSessionProcess 是投递到用户会话中的子进程句柄。
type UserSessionProcess struct {
	PID    uint32
	Handle windows.Handle
}

// Close 释放进程句柄，不影响子进程继续运行。
func (p *UserSessionProcess) Close() {
	if p != nil && p.Handle != 0 {
		windows.CloseHandle(p.Handle)
		p.Handle = 0
	}
}

// Wait 在 timeout 内等待子进程退出，超时返回 false。
func (p *UserSessionProcess) Wait(timeout time.Duration) bool {
	if p == nil || p.Handle == 0 {
		return true
	}
	milliseconds := timeout / time.Millisecond
	if milliseconds <= 0 {
		milliseconds = 1
	}
	event, err := windows.WaitForSingleObject(p.Handle, uint32(milliseconds))
	if err != nil {
		// 句柄异常时不阻塞调用方，按已退出处理。
		return true
	}
	return event == waitObjectEvent
}

// Terminate 强制结束子进程。它不会级联结束子进程再派生出的孙进程，
// 因此常驻型脚本必须自行 detach 启动。
func (p *UserSessionProcess) Terminate() {
	if p != nil && p.Handle != 0 {
		_ = windows.TerminateProcess(p.Handle, 1)
	}
}

// acquirePrimaryToken 取得可用于 CreateProcessAsUser 的主令牌。
func acquirePrimaryToken(session UserSession, mode TokenMode) (windows.Token, error) {
	var sourceToken windows.Token
	if mode == TokenAsSystem {
		err := windows.OpenProcessToken(windows.CurrentProcess(),
			windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &sourceToken)
		if err != nil {
			return 0, fmt.Errorf("打开服务进程令牌失败: %w", err)
		}
	} else {
		if err := windows.WTSQueryUserToken(session.SessionID, &sourceToken); err != nil {
			return 0, fmt.Errorf("获取%s的用户令牌失败(需要服务以SYSTEM身份运行): %w",
				session.DisplayName(), err)
		}
	}
	defer windows.CloseHandle(windows.Handle(sourceToken))

	var primary windows.Token
	err := windows.DuplicateTokenEx(sourceToken, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &primary)
	if err != nil {
		return 0, fmt.Errorf("复制主令牌失败: %w", err)
	}
	// SYSTEM 令牌属于 Session 0，必须显式改写它的会话号，否则 CreateProcessAsUser
	// 创建的进程仍留在服务会话里，登录用户的桌面上看不到任何窗口。
	// 改写会话号要求调用方持有 SeTcbPrivilege，LocalSystem 服务默认启用该特权。
	if mode == TokenAsSystem {
		sessionID := session.SessionID
		if err := windows.SetTokenInformation(primary, windows.TokenSessionId,
			(*byte)(unsafe.Pointer(&sessionID)), uint32(unsafe.Sizeof(sessionID))); err != nil {
			windows.CloseHandle(windows.Handle(primary))
			return 0, fmt.Errorf("把SYSTEM令牌切换到会话%d失败: %w", session.SessionID, err)
		}
	}
	return primary, nil
}

// composeCommandLine 拼接 Windows 命令行。syscall.ComposeCmdLine 在当前 Go 版本未导出，
// 这里用已导出的 syscall.EscapeArg 手工拼装。
func composeCommandLine(exePath string, args []string) string {
	var builder strings.Builder
	builder.WriteString(syscall.EscapeArg(exePath))
	for _, arg := range args {
		builder.WriteString(" ")
		builder.WriteString(syscall.EscapeArg(arg))
	}
	return builder.String()
}

// LaunchInUserSession 在指定会话的交互式桌面上启动进程。
// workDir 为空时子进程使用系统默认工作目录。
func LaunchInUserSession(session UserSession, mode TokenMode, exePath string, args []string, workDir string) (*UserSessionProcess, error) {
	if strings.TrimSpace(exePath) == "" {
		return nil, errors.New("未指定要启动的程序")
	}

	primary, err := acquirePrimaryToken(session, mode)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(windows.Handle(primary))

	// 用户令牌需要单独构造环境块，否则子进程拿到的是 SYSTEM 的环境变量；
	// SYSTEM 模式传 nil，让子进程继承服务自身环境即可。
	var envBlock *uint16
	if mode == TokenAsLoggedOnUser {
		if err := windows.CreateEnvironmentBlock(&envBlock, primary, false); err != nil {
			return nil, fmt.Errorf("创建用户环境块失败: %w", err)
		}
		defer windows.DestroyEnvironmentBlock(envBlock)
	}

	appName, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, err
	}
	commandLine, err := windows.UTF16PtrFromString(composeCommandLine(exePath, args))
	if err != nil {
		return nil, err
	}
	desktop, err := windows.UTF16PtrFromString(interactiveDesktop)
	if err != nil {
		return nil, err
	}
	var currentDir *uint16
	if strings.TrimSpace(workDir) != "" {
		if currentDir, err = windows.UTF16PtrFromString(workDir); err != nil {
			return nil, err
		}
	}

	startupInfo := windows.StartupInfo{Desktop: desktop}
	startupInfo.Cb = uint32(unsafe.Sizeof(startupInfo))

	var processInfo windows.ProcessInformation
	creationFlags := uint32(createUnicodeEnvironmentFlag | createNoWindowFlag)
	err = windows.CreateProcessAsUser(primary, appName, commandLine, nil, nil, false,
		creationFlags, envBlock, currentDir, &startupInfo, &processInfo)
	if err != nil {
		return nil, fmt.Errorf("在%s中启动进程失败: %w", session.DisplayName(), err)
	}

	windows.CloseHandle(processInfo.Thread)
	return &UserSessionProcess{PID: processInfo.ProcessId, Handle: processInfo.Process}, nil
}

// JikeAgentDataDir 返回 %ProgramData%\JikeAgent。所有需要跨会话共享的文件都放在这里，
// 因为服务的工作目录位于 System32，普通用户无权访问。
func JikeAgentDataDir() (string, error) {
	programData := strings.TrimSpace(os.Getenv("ProgramData"))
	if programData == "" {
		systemDrive := strings.TrimSpace(os.Getenv("SystemDrive"))
		if systemDrive == "" {
			systemDrive = "C:"
		}
		programData = systemDrive + string(filepath.Separator) + "ProgramData"
	}
	root := filepath.Join(programData, jikeAgentDataDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建数据目录失败: %w", err)
	}
	return root, nil
}

// ensureSubDir 创建数据目录下的子目录，必要时放宽 Users 权限。
func ensureSubDir(name string, grantUsers bool) (string, error) {
	root, err := JikeAgentDataDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	if !grantUsers {
		return path, nil
	}
	if _, done := grantedUserShare.Load(path); done {
		return path, nil
	}
	if err := grantUsersModifyOnDir(path); err != nil {
		return "", err
	}
	grantedUserShare.Store(path, struct{}{})
	return path, nil
}

// AssetsDir 存放 SYSTEM 写入、普通用户只读的资源，例如壁纸图片。
func AssetsDir() (string, error) {
	return ensureSubDir("assets", false)
}

// StateDir 存放 agent 自身的状态文件，例如水印配置。普通用户只读不可写，
// 因此无法通过修改配置来关闭水印。
func StateDir() (string, error) {
	return ensureSubDir("state", false)
}

// SystemTempDir 仅供 SYSTEM 令牌模式使用：SYSTEM 写入脚本、SYSTEM 执行脚本。
func SystemTempDir() (string, error) {
	return ensureSubDir("tmp-system", false)
}

// UserShareDir 供用户令牌模式使用，需要 Users 可写才能让会话内的 PowerShell
// 读取脚本并写回执行结果。
//
// 铁律：SYSTEM 身份绝不从本目录加载脚本或配置，否则普通用户替换文件内容
// 即可把自己的代码提到 SYSTEM 权限执行。
func UserShareDir() (string, error) {
	return ensureSubDir("tmp-user", true)
}

// grantUsersModifyOnDir 为目录追加 Users 组的可继承修改权限。
// 合并基准传 nil，配合 UNPROTECTED_DACL_SECURITY_INFORMATION，系统仍会从父目录
// 继承原有的 SYSTEM/Administrators/Users 条目，不会削弱既有权限。
func grantUsersModifyOnDir(path string) error {
	usersSID, err := windows.StringToSid(wellKnownUsersSID)
	if err != nil {
		return fmt.Errorf("解析Users组SID失败: %w", err)
	}
	// StringToSid 内部已经 LocalFree 了系统分配的那份 SID，返回的是 Go 堆上的副本，
	// 这里同样不能再 LocalFree，否则又是一个堆损坏。

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(usersSID),
			},
		},
	}

	newACL, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("构建ACL失败: %w", err)
	}
	// 这里绝不能 LocalFree：ACLFromEntries 返回的 ACL 位于 Go 堆上（由 GC 管理），
	// 把它交给 Win32 堆管理器会破坏进程堆，服务进程会以 STATUS_HEAP_CORRUPTION
	// (0xC0000374) 静默死亡——SCM 只报"意外停止"，既不生成 WER 报告，
	// runtime 的 fatal 文本也因为服务没有 stderr 而彻底丢失。

	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo, nil, nil, newACL, nil); err != nil {
		return fmt.Errorf("写入目录ACL失败: %w", err)
	}
	return nil
}

// ResolvePowerShellPath 返回绝对路径的 PowerShell 可执行文件。
// 投递到用户会话时子进程的 PATH 由新建的环境块决定，必须使用绝对路径。
func ResolvePowerShellPath() string {
	if bundled := bundledPwshPath(); bundled != "" {
		return bundled
	}
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "powershell.exe"
}

// bundledPwshPath 把 getPowerShellCommand 使用的相对路径 ./pwsh/pwsh.exe 转成绝对路径。
func bundledPwshPath() string {
	for _, name := range []string{"pwsh.exe", "pwsh"} {
		absolute, err := filepath.Abs(filepath.Join(".", "pwsh", name))
		if err != nil {
			continue
		}
		if _, statErr := os.Stat(absolute); statErr == nil {
			return absolute
		}
	}
	return ""
}

// CurrentExecutablePath 返回 agent 自身的绝对路径，用于把自己作为子进程投递到用户会话。
func CurrentExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径失败: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return exePath, nil
	}
	return resolved, nil
}

// userSessionSupported 报告当前平台是否支持用户会话投递。
func userSessionSupported() bool { return true }
