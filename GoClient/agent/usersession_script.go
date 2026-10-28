package agent

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// UserSessionDirective 是投递到登录用户会话执行的指令行标记。
// 服务端下发时在脚本首行插入该标记，客户端据此改走 CreateProcessAsUser 路径，
// 因此不需要改动 ExecutePowershellScript 的 SignalR 方法签名，新老版本互相兼容。
const UserSessionDirective = "#JK-USERSESSION"

const (
	// userSessionScriptTimeout 与 ExecuteScriptNatively 的 30 秒预算保持一致。
	// 服务端 AgentService 为该指令预留 60 秒回调上限，比这里更宽才不会抢先判超时。
	userSessionScriptTimeout = 30 * time.Second
	// userSessionNoWaitGrace 是 -NoWait 模式下等待启动失败的宽限时间，
	// 只用于捕获"进程起来就崩了"的情况，不等待脚本执行完成。
	userSessionNoWaitGrace = 2 * time.Second
	// userSessionFileRetention 是遗留文件的保留期，超过后由 sweepStaleFiles 回收。
	userSessionFileRetention = 24 * time.Hour
)

// userSessionScriptRequest 描述一次用户会话脚本执行请求。
type userSessionScriptRequest struct {
	Script     string
	AsSystem   bool
	NoWait     bool
	SessionID  uint32
	HasSession bool
}

// isUserSessionCommand 判断脚本是否要求投递到用户会话执行。
func isUserSessionCommand(script string) bool {
	_, ok := parseUserSessionDirective(script)
	return ok
}

// parseUserSessionDirective 解析脚本首行的指令及其标志，返回剩余的真正脚本内容。
// 支持的标志：-System（以 SYSTEM 身份运行，普通用户无法结束）、-NoWait（不等待返回）、
// -Session <id>（指定会话，默认使用物理控制台会话）。
func parseUserSessionDirective(script string) (*userSessionScriptRequest, bool) {
	lines := splitScriptLines(script)

	index := 0
	for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
		index++
	}
	if index >= len(lines) {
		return nil, false
	}

	fields := strings.Fields(strings.TrimSpace(stripBOM(lines[index])))
	if len(fields) == 0 || !strings.EqualFold(fields[0], UserSessionDirective) {
		return nil, false
	}

	request := &userSessionScriptRequest{}
	for i := 1; i < len(fields); i++ {
		flag := strings.TrimLeft(fields[i], "-/")
		switch {
		case strings.EqualFold(flag, "System"):
			request.AsSystem = true
		case strings.EqualFold(flag, "NoWait"):
			request.NoWait = true
		case strings.EqualFold(flag, "Session"):
			if i+1 >= len(fields) {
				return nil, false
			}
			i++
			sessionID, err := strconv.ParseUint(fields[i], 10, 32)
			if err != nil {
				return nil, false
			}
			request.SessionID = uint32(sessionID)
			request.HasSession = true
		default:
			// 未知标志直接判定为不是本指令，避免把普通注释误当指令吃掉。
			return nil, false
		}
	}

	request.Script = strings.Join(lines[index+1:], "\n")
	if strings.TrimSpace(request.Script) == "" {
		return nil, false
	}
	return request, true
}

// splitScriptLines 按 \r\n 或 \n 切分脚本，保留空行。
func splitScriptLines(script string) []string {
	normalized := strings.ReplaceAll(script, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

// stripBOM 去掉服务端可能带上的 UTF-8 BOM，否则首行标记比对会失败。
func stripBOM(line string) string {
	return strings.TrimPrefix(line, "\ufeff")
}

// scriptWorkDir 按令牌模式选择脚本落盘目录。
//
// SYSTEM 身份只能使用未放宽权限的 tmp-system：如果 SYSTEM 去执行 Users 可写目录里的
// 脚本，普通用户就能通过替换文件内容把自己的代码提权到 SYSTEM 执行。
func scriptWorkDir(mode TokenMode) (string, error) {
	if mode == TokenAsSystem {
		return SystemTempDir()
	}
	return UserShareDir()
}

// ExecuteScriptInUserSession 把 PowerShell 脚本投递到登录用户的交互式会话中执行。
// 服务进程运行在 Session 0，直接调用 SystemParametersInfo 或创建窗口对用户都不可见，
// 必须先定位当前登录会话再用 CreateProcessAsUser 投递。
func ExecuteScriptInUserSession(script string) string {
	request, ok := parseUserSessionDirective(script)
	if !ok {
		return fmt.Sprintf("脚本缺少有效的 %s 指令行，或指令后没有脚本内容。", UserSessionDirective)
	}
	if !userSessionSupported() {
		return "用户会话投递仅支持 Windows 平台。"
	}

	session, err := ResolveTargetSession(request.SessionID, request.HasSession)
	if err != nil {
		return err.Error()
	}

	mode := TokenAsLoggedOnUser
	identity := "登录用户身份"
	if request.AsSystem {
		mode = TokenAsSystem
		identity = "SYSTEM身份"
	}

	workDir, err := scriptWorkDir(mode)
	if err != nil {
		return err.Error()
	}
	sweepStaleFiles(workDir, userSessionFileRetention)

	fileID := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Int63())
	scriptPath := filepath.Join(workDir, fileID+".ps1")
	wrapperPath := filepath.Join(workDir, fileID+".wrapper.ps1")
	outPath := filepath.Join(workDir, fileID+".out.txt")
	errPath := filepath.Join(workDir, fileID+".err.txt")

	if err := writeFileWithBOM(scriptPath, request.Script); err != nil {
		return "写入脚本文件失败: " + err.Error()
	}
	if err := writeFileWithBOM(wrapperPath, buildWrapperScript(scriptPath, outPath, errPath)); err != nil {
		_ = os.Remove(scriptPath)
		return "写入wrapper脚本失败: " + err.Error()
	}

	powerShell := ResolvePowerShellPath()
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", wrapperPath}
	process, err := LaunchInUserSession(session, mode, powerShell, args, workDir)
	if err != nil {
		removeUserSessionFiles(scriptPath, wrapperPath, outPath, errPath)
		return fmt.Sprintf("投递到%s失败: %s", session.DisplayName(), err.Error())
	}
	defer process.Close()

	waitBudget := userSessionScriptTimeout
	if request.NoWait {
		waitBudget = userSessionNoWaitGrace
	}
	exited := process.Wait(waitBudget)

	output := ""
	if exited {
		output = joinUserSessionOutput(readFileIfExists(outPath), readFileIfExists(errPath))
		removeUserSessionFiles(scriptPath, wrapperPath, outPath, errPath)
		if output == "" {
			return "执行结果为空。"
		}
		return output
	}

	// 进程仍在运行：-NoWait 属于预期行为，不能终止；默认模式按超时终止。
	// 未退出时脚本文件可能仍被占用，留给 sweepStaleFiles 回收。
	output = joinUserSessionOutput(readFileIfExists(outPath), readFileIfExists(errPath))
	if request.NoWait {
		message := fmt.Sprintf("已在%s中以%s启动，PID=%d，未等待执行完成。",
			session.DisplayName(), identity, process.PID)
		if output != "" {
			message += "\n" + output
		}
		return message
	}

	process.Terminate()
	removeUserSessionFiles(scriptPath, wrapperPath, outPath, errPath)
	message := fmt.Sprintf("在%s中执行超时(%d秒)，已终止执行。",
		session.DisplayName(), int(userSessionScriptTimeout/time.Second))
	if output != "" {
		message += "\n" + output
	}
	return message
}

// joinUserSessionOutput 合并标准输出与错误输出，编码处理与 ExecuteScriptNatively 保持一致。
func joinUserSessionOutput(outBytes, errBytes []byte) string {
	output := decodeWithBomAndFallback(outBytes)
	errorOutput := decodeWithBomAndFallback(errBytes)
	if errorOutput == "" {
		return output
	}
	if output == "" {
		return errorOutput
	}
	return output + "\n" + errorOutput
}

// removeUserSessionFiles 清理本次执行产生的临时文件。
func removeUserSessionFiles(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// sweepStaleFiles 回收超过保留期的遗留文件。-NoWait 模式下脚本文件不能在进程仍在使用时删除，
// 靠这里兜底，避免目录无限增长。
func sweepStaleFiles(dir string, olderThan time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
