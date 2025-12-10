package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// ExecuteScriptNatively 执行PowerShell脚本
func ExecuteScriptNatively(script string) string {
	tempDir, err := os.Getwd()
	if err != nil {
		return "无法获取当前目录: " + err.Error()
	}

	// 使用纳秒时间戳+随机数生成唯一ID
	fileID := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Int63())
	scriptFile := fileID + ".ps1"
	scriptPath := filepath.Join(tempDir, scriptFile)

	outPath := filepath.Join(tempDir, fileID+".out.txt")
	errPath := filepath.Join(tempDir, fileID+".err.txt")

	wrapperFile := fileID + ".wrapper.ps1"
	wrapperPath := filepath.Join(tempDir, wrapperFile)

	// 写入目标脚本（带 BOM 的 UTF-8）
	if err := writeFileWithBOM(scriptPath, script); err != nil {
		return "写入脚本文件失败: " + err.Error()
	}

	// 创建 wrapper 脚本
	wrapperContent := buildWrapperScript(scriptPath, outPath, errPath)
	if err := writeFileWithBOM(wrapperPath, wrapperContent); err != nil {
		return "写入wrapper脚本失败: " + err.Error()
	}

	// 清理临时文件
	defer func() {
		_ = os.Remove(scriptPath)
		_ = os.Remove(wrapperPath)
		_ = os.Remove(outPath)
		_ = os.Remove(errPath)
	}()

	// 确定PowerShell命令
	cmd := getPowerShellCommand()
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", wrapperPath}

	// 创建30秒超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 执行命令
	proc := exec.CommandContext(ctx, cmd, args...)
	proc.Dir = tempDir
	if err := proc.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "PowerShell脚本执行超时(30秒)，已终止执行。"
		}
		// 忽略其他执行错误，因为脚本可能正常返回非零退出码
	}

	// 读取输出
	outBytes := readFileIfExists(outPath)
	errBytes := readFileIfExists(errPath)

	output := decodeWithBomAndFallback(outBytes)
	errorOutput := decodeWithBomAndFallback(errBytes)

	if errorOutput != "" {
		if output == "" {
			return errorOutput
		}
		return output + "\n" + errorOutput
	}

	if output == "" {
		return "执行结果为空。"
	}

	return output
}

// writeFileWithBOM 写入带BOM的UTF-8文件
func writeFileWithBOM(path, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 写入 UTF-8 BOM
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	_, err = f.WriteString(content)
	return err
}

// buildWrapperScript 构建wrapper脚本
func buildWrapperScript(scriptPath, outPath, errPath string) string {
	escapedScriptPath := escapeForSingleQuotedPowerShell(scriptPath)
	escapedOutPath := escapeForSingleQuotedPowerShell(outPath)
	escapedErrPath := escapeForSingleQuotedPowerShell(errPath)

	return `[Console]::OutputEncoding=[System.Text.Encoding]::UTF8
$OutputEncoding=[System.Text.Encoding]::UTF8
try {
    & '` + escapedScriptPath + `' 2>&1 | Out-File -FilePath '` + escapedOutPath + `' -Encoding UTF8
    exit $LASTEXITCODE
} catch {
    $_ | Out-File -FilePath '` + escapedErrPath + `' -Encoding UTF8
    exit 1
}`
}

// escapeForSingleQuotedPowerShell 转义PowerShell单引号字符串
func escapeForSingleQuotedPowerShell(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// readFileIfExists 读取文件（如果存在）
func readFileIfExists(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte{}
	}
	return data
}

// decodeWithBomAndFallback 使用BOM或启发式方法解码字节
func decodeWithBomAndFallback(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// 检查 UTF-8 BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:])
	}

	// 检查 UTF-16 LE BOM
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return decodeUTF16LE(data[2:])
	}

	// 检查 UTF-16 BE BOM
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return decodeUTF16BE(data[2:])
	}

	// 启发式：大量零字节可能是 UTF-16 LE
	zeroCount := 0
	for _, b := range data {
		if b == 0 {
			zeroCount++
		}
	}
	if zeroCount > len(data)/4 {
		decoded := decodeUTF16LE(data)
		if decoded != "" {
			return decoded
		}
	}

	// 尝试 UTF-8
	if utf8.Valid(data) {
		return string(data)
	}

	// 回退到原始字符串（可能包含替换字符）
	return string(data)
}

// decodeUTF16LE 解码 UTF-16 LE
func decodeUTF16LE(data []byte) string {
	if len(data)%2 != 0 {
		// 长度不是偶数，添加一个零字节
		data = append(data, 0)
	}

	u16s := make([]uint16, len(data)/2)
	for i := 0; i < len(u16s); i++ {
		u16s[i] = uint16(data[i*2]) | uint16(data[i*2+1])<<8
	}

	return string(utf16Decode(u16s))
}

// decodeUTF16BE 解码 UTF-16 BE
func decodeUTF16BE(data []byte) string {
	if len(data)%2 != 0 {
		data = append(data, 0)
	}

	u16s := make([]uint16, len(data)/2)
	for i := 0; i < len(u16s); i++ {
		u16s[i] = uint16(data[i*2])<<8 | uint16(data[i*2+1])
	}

	return string(utf16Decode(u16s))
}

// utf16Decode 解码 UTF-16 为 rune 切片
func utf16Decode(u16 []uint16) []rune {
	var runes []rune
	for i := 0; i < len(u16); i++ {
		r := rune(u16[i])
		if r >= 0xD800 && r <= 0xDBFF && i+1 < len(u16) {
			// 高代理项，需要配对
			r2 := rune(u16[i+1])
			if r2 >= 0xDC00 && r2 <= 0xDFFF {
				// 组合代理对
				r = ((r - 0xD800) << 10) + (r2 - 0xDC00) + 0x10000
				i++
			}
		}
		runes = append(runes, r)
	}
	return runes
}

// getPowerShellCommand 获取PowerShell命令路径
func getPowerShellCommand() string {
	if runtime.GOOS == "linux" {
		// Linux系统优先使用本地pwsh
		if _, err := os.Stat("./pwsh/pwsh"); err == nil {
			return "./pwsh/pwsh"
		}
		// 回退到系统bash
		return "bash"
	}

	// Windows系统
	if _, err := os.Stat("./pwsh/pwsh.exe"); err == nil {
		return "./pwsh/pwsh.exe"
	}
	return "powershell"
}

// ReadScriptOutput 读取脚本输出（用于测试）
func ReadScriptOutput(r io.Reader) (string, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", err
	}
	return decodeWithBomAndFallback(buf.Bytes()), nil
}
