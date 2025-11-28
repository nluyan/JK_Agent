package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const serviceName = "JikeAgent"

func main() {
	baseDirectory, err := os.Getwd()
	if err != nil {
		logError("获取当前目录失败: %v", err)
		return
	}

	// 切换到基础目录
	if err := os.Chdir(baseDirectory); err != nil {
		logError("切换目录失败: %v", err)
		return
	}

	logInfo("启动更新...")

	defer func() {
		// 无论成功失败，最后都尝试启动服务
		startService(serviceName)
	}()

	// 检查临时更新文件夹是否存在
	tempDir := filepath.Join(baseDirectory, "temp")
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		logError("没有找到临时更新文件夹: %s", tempDir)
		return
	}

	// 停止服务
	logInfo("停止服务: %s", serviceName)
	if err := stopService(serviceName); err != nil {
		logError("停止服务失败: %v", err)
	}

	// 等待5秒
	logInfo("等待5秒...")
	time.Sleep(5 * time.Second)

	// 复制文件
	logInfo("开始复制文件从 %s 到 %s", tempDir, baseDirectory)
	if err := copyDirectory(tempDir, baseDirectory, true); err != nil {
		logError("复制目录失败: %v", err)
		return
	}

	logInfo("更新完成")
}

// stopService 使用 sc.exe 停止Windows服务
func stopService(name string) error {
	cmd := exec.Command("sc.exe", "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 检查是否已经停止
		if strings.Contains(string(output), "已经停止") || strings.Contains(string(output), "1062") {
			logInfo("服务已经停止")
			return nil
		}
		return fmt.Errorf("停止服务失败: %w, 输出: %s", err, string(output))
	}

	logInfo("服务停止命令已发送: %s", strings.TrimSpace(string(output)))
	return nil
}

// startService 使用 sc.exe 启动Windows服务
func startService(name string) error {
	logInfo("启动服务: %s", name)

	cmd := exec.Command("sc.exe", "start", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 检查是否已经运行
		if strings.Contains(string(output), "已经运行") || strings.Contains(string(output), "1056") {
			logInfo("服务已经在运行")
			return nil
		}
		logError("启动服务失败: %v, 输出: %s", err, string(output))
		return err
	}

	logInfo("服务启动成功: %s", strings.TrimSpace(string(output)))
	return nil
}

// copyDirectory 递归复制目录（简化版本）
func copyDirectory(src, dst string, overwrite bool) error {
	// 规范化路径并检查源目录
	srcDir, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("获取源目录绝对路径失败: %w", err)
	}

	dstDir, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("获取目标目录绝对路径失败: %w", err)
	}

	// 使用 filepath.WalkDir 进行递归遍历
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// 计算相对路径
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// 跳过根目录自身
		if relPath == "." {
			return nil
		}

		// 目标路径
		dstPath := filepath.Join(dstDir, relPath)

		if d.IsDir() {
			// 创建目录
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				logError("创建目录失败 %s: %v", dstPath, err)
			}
		} else {
			// 复制文件
			if err := copyFile(path, dstPath, overwrite); err != nil {
				logError("复制文件失败 %s: %v", path, err)
			}
		}

		return nil
	})
}

// copyFile 复制单个文件
func copyFile(src, dst string, overwrite bool) error {
	// 检查目标文件是否存在
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("目标文件已存在: %s", dst)
		}
	}

	// 打开源文件
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer srcFile.Close()

	// 创建目标文件
	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer dstFile.Close()

	// 复制内容
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("复制文件内容失败: %w", err)
	}

	// 复制权限
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("获取源文件信息失败: %w", err)
	}

	if err := os.Chmod(dst, srcInfo.Mode()); err != nil {
		// 权限复制失败不影响主流程
		logError("设置文件权限失败 %s: %v", dst, err)
	}

	return nil
}

// logInfo 记录信息日志
func logInfo(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logMsg := fmt.Sprintf("%s [INFO] %s\n", timestamp, msg)

	// 输出到控制台
	fmt.Print(logMsg)

	// 追加到日志文件
	appendLog(logMsg)
}

// logError 记录错误日志
func logError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logMsg := fmt.Sprintf("%s [ERROR] %s\n", timestamp, msg)

	// 输出到控制台
	fmt.Fprint(os.Stderr, logMsg)

	// 追加到日志文件
	appendLog(logMsg)
}

// appendLog 追加日志到文件
func appendLog(msg string) {
	baseDir, err := os.Getwd()
	if err != nil {
		return
	}

	logDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}

	logFile := filepath.Join(logDir, "update_log.txt")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.WriteString(msg)
}
