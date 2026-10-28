package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// writeFileAtomic 先写临时文件再替换目标，避免读到半截内容。
// 壁纸图片与水印状态文件都用它落盘。
func writeFileAtomic(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

// writeResultFile 把子进程的执行结果写回共享目录，供服务进程读取。
// 跨会话的 stdout 无法直接继承，因此统一用文件回传。
func writeResultFile(path string, text string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	_ = writeFileAtomic(path, []byte(text))
}

// parseChildFlags 解析子进程的 -key value 形式参数。
// 重复出现的键以最后一次为准，缺少值的键记为空串。
func parseChildFlags(args []string) map[string]string {
	values := make(map[string]string, len(args)/2+1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		key := strings.TrimLeft(arg, "-")
		if key == "" {
			continue
		}
		if i+1 < len(args) && !isFlagToken(args[i+1]) {
			values[key] = args[i+1]
			i++
			continue
		}
		values[key] = ""
	}
	return values
}

// isFlagToken 判断一个参数是不是标志名。只有纯字母的名字才算标志：
// 负数（例如水印的 -30 度倾角）和以 "-" 开头的文案同样用 "-" 起头，但都是取值，
// 必须区分开，否则会被当成新标志把真正的取值吃掉。
func isFlagToken(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	trimmed := strings.TrimLeft(arg, "-")
	if trimmed == "" {
		return false
	}
	for _, char := range trimmed {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return false
		}
	}
	return true
}

// flagInt 读取整型参数，缺省或非法时返回 fallback。
func flagInt(values map[string]string, key string, fallback int) int {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

// flagBool 读取 0/1 形式的布尔参数。
func flagBool(values map[string]string, key string) bool {
	return strings.TrimSpace(values[key]) == "1"
}

// childResultPath 返回子进程回传结果的文件路径参数名。
const childResultFlag = "result"

// maxCommandLogRunes 是原生命令写入日志的最大字符数。
// 壁纸命令可以内嵌 base64 图片，直接打印会把数兆字节写进日志文件。
const maxCommandLogRunes = 512

// logScriptSummary 生成适合写日志的脚本摘要，必要时截断。
func logScriptSummary(script string) string {
	trimmed := strings.TrimSpace(script)
	if !isWallpaperCommand(trimmed) && !isWatermarkCommand(trimmed) {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxCommandLogRunes {
		return trimmed
	}
	return string(runes[:maxCommandLogRunes]) +
		fmt.Sprintf("...(共%d字符，已截断)", len(runes))
}

// errNoLoggedOnUser 在找不到已登录用户时返回，服务端据此提示运维。
var errNoLoggedOnUser = errors.New("当前没有已登录的用户会话")

// errUserSessionUnsupported 在非 Windows 平台返回。会话隔离与交互式桌面投递
// 是 Windows 特有的机制，Linux 客户端由 systemd 直接以登录用户身份运行，不需要该能力。
var errUserSessionUnsupported = errors.New("用户会话投递仅支持 Windows 平台")

// exitUnsupported 用于在非 Windows 平台上给出一致的错误文本。
func exitUnsupported(feature string) string {
	return fmt.Sprintf("%s仅支持 Windows 平台。", feature)
}
