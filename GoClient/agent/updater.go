package agent

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"jkagent/goclient/config"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Updater 自动更新管理器
type Updater struct {
	serverURL         string
	group             string
	logger            zerolog.Logger
	checkInterval     time.Duration
	isCheckingUpdate  bool
	updateMutex       sync.Mutex
	stopChan          chan struct{}
	ctx               context.Context
	onCheckUpdateFunc func()
}

// NewUpdater 创建新的Updater实例
func NewUpdater(serverURL, group string, checkInterval time.Duration, logger zerolog.Logger) *Updater {
	if checkInterval < time.Minute {
		checkInterval = 10 * time.Minute // 默认10分钟
	}

	return &Updater{
		serverURL:     serverURL,
		group:         group,
		logger:        logger,
		checkInterval: checkInterval,
		stopChan:      make(chan struct{}),
	}
}

// SetOnCheckUpdate 设置手动检查更新回调
func (u *Updater) SetOnCheckUpdate(handler func()) {
	u.onCheckUpdateFunc = handler
}

// Start 启动定期更新检查
func (u *Updater) Start(ctx context.Context) {
	u.ctx = ctx
	u.logger.Debug().
		Float64("intervalMinutes", u.checkInterval.Minutes()).
		Msg("启动定期更新检查")

	// 启动定期检查协程
	go u.periodicCheck()
}

// Stop 停止定期更新检查
func (u *Updater) Stop() {
	close(u.stopChan)
}

// CheckNow 手动触发更新检查
func (u *Updater) CheckNow() {
	u.logger.Debug().Msg("收到手动更新检查请求")
	if err := u.checkAndUpdate(); err != nil {
		u.logger.Error().Err(err).Msg("手动更新检查失败")
	}
}

// periodicCheck 定期检查更新
func (u *Updater) periodicCheck() {
	ticker := time.NewTicker(u.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := u.checkAndUpdate(); err != nil {
				u.logger.Debug().Err(err).Msg("定期更新检查失败")
			}
		case <-u.stopChan:
			u.logger.Debug().Msg("定期更新检查已停止")
			return
		case <-u.ctx.Done():
			u.logger.Debug().Msg("定期更新检查上下文取消")
			return
		}
	}
}

// checkAndUpdate 检查并执行更新
func (u *Updater) checkAndUpdate() error {
	u.updateMutex.Lock()
	if u.isCheckingUpdate {
		u.updateMutex.Unlock()
		return nil
	}
	u.isCheckingUpdate = true
	u.updateMutex.Unlock()

	defer func() {
		u.updateMutex.Lock()
		u.isCheckingUpdate = false
		u.updateMutex.Unlock()
	}()

	if u.serverURL == "" {
		u.logger.Debug().Msg("ServerURL为空，跳过更新检查")
		return nil
	}

	// 获取远程版本号
	remoteVersionURL := fmt.Sprintf("%s/update/%s/version.txt", u.serverURL, u.group)
	remoteVersion, err := u.fetchRemoteVersion(remoteVersionURL)
	if err != nil {
		return fmt.Errorf("获取远程版本失败: %w", err)
	}

	if remoteVersion == "" {
		u.logger.Debug().Msg("远程版本信息为空，跳过更新")
		return nil
	}

	// 比较版本
	if !u.isNewVersion(remoteVersion, config.Default.Version) {
		return nil
	}

	u.logger.Info().
		Str("remoteVersion", remoteVersion).
		Str("currentVersion", config.Default.Version).
		Msg("发现新版本，开始下载更新")

	// 执行更新流程
	return u.performUpdate()
}

// fetchRemoteVersion 获取远程版本号
func (u *Updater) fetchRemoteVersion(url string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

// isNewVersion 比较版本号（语义化版本）
func (u *Updater) isNewVersion(remoteVersion, currentVersion string) bool {
	remoteParts := parseVersion(remoteVersion)
	currentParts := parseVersion(currentVersion)

	// 比较主版本、次版本、修订版本
	for i := 0; i < 3; i++ {
		if remoteParts[i] > currentParts[i] {
			return true
		}
		if remoteParts[i] < currentParts[i] {
			return false
		}
	}

	// 版本号完全相同
	return false
}

// parseVersion 解析版本号字符串为 [major, minor, patch]
func parseVersion(version string) [3]int {
	var parts [3]int

	// 移除前缀 'v' 或 'V'
	version = strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")

	// 按点分割
	segments := strings.Split(version, ".")

	for i := 0; i < 3 && i < len(segments); i++ {
		// 解析数字部分（忽略非数字后缀，如 "1.0.0-beta"）
		numStr := segments[i]
		for j, ch := range numStr {
			if ch < '0' || ch > '9' {
				numStr = numStr[:j]
				break
			}
		}

		if num, err := strconv.Atoi(numStr); err == nil {
			parts[i] = num
		}
	}

	return parts
}

// performUpdate 执行更新流程
func (u *Updater) performUpdate() error {
	baseDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取工作目录失败: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows平台更新流程
		return u.performWindowsUpdate(baseDir)
	}

	// Linux平台更新流程
	return u.performLinuxUpdate(baseDir)
}

// performWindowsUpdate Windows平台更新
func (u *Updater) performWindowsUpdate(baseDir string) error {
	// 下载更新器
	updaterURL := fmt.Sprintf("%s/update/%s/Updater.exe", u.serverURL, u.group)
	updaterPath := filepath.Join(baseDir, "Updater.exe")
	if err := u.downloadFile(updaterURL, updaterPath); err != nil {
		return fmt.Errorf("下载Updater.exe失败: %w", err)
	}

	// 下载更新包
	packageURL := fmt.Sprintf("%s/update/%s/AgentClient.zip", u.serverURL, u.group)
	packagePath := filepath.Join(baseDir, "AgentClient.zip")
	if err := u.downloadFile(packageURL, packagePath); err != nil {
		return fmt.Errorf("下载AgentClient.zip失败: %w", err)
	}

	// 删除旧的临时目录
	tempDir := filepath.Join(baseDir, "temp")
	u.logger.Info().Msg("删除旧的临时目录")
	if err := os.RemoveAll(tempDir); err != nil {
		u.logger.Warn().Err(err).Msg("删除临时目录失败")
	}

	// 解压更新包
	u.logger.Info().Msg("解压更新包")
	if err := u.unzip(packagePath, tempDir); err != nil {
		return fmt.Errorf("解压更新包失败: %w", err)
	}

	// 删除更新包
	u.logger.Info().Msg("删除更新包")
	if err := os.Remove(packagePath); err != nil {
		u.logger.Warn().Err(err).Msg("删除更新包失败")
	}

	// 启动更新程序
	u.logger.Info().Msg("启动更新程序")
	cmd := exec.Command(updaterPath)
	cmd.Dir = baseDir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动Updater.exe失败: %w", err)
	}

	// 让更新程序启动
	time.Sleep(1 * time.Second)

	// 退出当前程序（由更新程序接管）
	u.logger.Info().Msg("更新程序已启动，当前程序将退出")

	return nil
}

// performLinuxUpdate Linux平台更新
func (u *Updater) performLinuxUpdate(baseDir string) error {
	// 下载更新包
	packageURL := fmt.Sprintf("%s/update/%s/AgentClient.zip", u.serverURL, u.group)
	packagePath := filepath.Join(baseDir, "AgentClient.zip")
	if err := u.downloadFile(packageURL, packagePath); err != nil {
		return fmt.Errorf("下载AgentClient.zip失败: %w", err)
	}

	// 删除旧的临时目录
	tempDir := filepath.Join(baseDir, "temp")
	u.logger.Info().Msg("删除旧的临时目录")
	if err := os.RemoveAll(tempDir); err != nil {
		u.logger.Warn().Err(err).Msg("删除临时目录失败")
	}

	// 解压更新包
	u.logger.Info().Msg("解压更新包")
	if err := u.unzip(packagePath, tempDir); err != nil {
		return fmt.Errorf("解压更新包失败: %w", err)
	}

	// 删除更新包
	u.logger.Info().Msg("删除更新包")
	if err := os.Remove(packagePath); err != nil {
		u.logger.Warn().Err(err).Msg("删除更新包失败")
	}

	// 复制文件
	u.logger.Info().Msg("复制文件")
	if err := u.copyDirectory(tempDir, baseDir); err != nil {
		return fmt.Errorf("复制文件失败: %w", err)
	}

	// 设置可执行权限
	execPath := filepath.Join(baseDir, "AgentClient")
	u.logger.Info().Msg("设置可执行权限")
	cmd := exec.Command("chmod", "+x", execPath)
	if err := cmd.Run(); err != nil {
		u.logger.Warn().Err(err).Msg("设置可执行权限失败")
	}

	// 退出应用（systemd会自动重启）
	u.logger.Info().Msg("更新完成，程序将退出")
	os.Exit(0)

	return nil
}

// downloadFile 下载文件
func (u *Updater) downloadFile(url, filePath string) error {
	if url == "" || filePath == "" {
		return fmt.Errorf("URL或文件路径不能为空")
	}

	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	// 创建目录
	dir := filepath.Dir(filePath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	// 写入文件
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	u.logger.Info().Str("path", filePath).Msg("文件下载成功")
	return nil
}

// unzip 解压ZIP文件
func (u *Updater) unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		if err := u.extractFile(f, dest); err != nil {
			return err
		}
	}

	return nil
}

// extractFile 提取单个文件
func (u *Updater) extractFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	path := filepath.Join(dest, f.Name)

	// 检查路径遍历安全性
	if !strings.HasPrefix(path, filepath.Clean(dest)+string(os.PathSeparator)) {
		return fmt.Errorf("非法文件路径: %s", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(path, f.Mode())
	}

	// 创建父目录
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	// 创建文件
	outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

// copyDirectory 递归复制目录
func (u *Updater) copyDirectory(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !srcInfo.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", src)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	// 确保目标目录存在
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// 递归复制子目录
			if err := u.copyDirectory(srcPath, dstPath); err != nil {
				u.logger.Warn().Err(err).Str("dir", srcPath).Msg("复制目录失败")
			}
		} else {
			// 复制文件
			if err := u.copyFile(srcPath, dstPath); err != nil {
				u.logger.Warn().Err(err).Str("file", srcPath).Msg("复制文件失败")
			}
		}
	}

	return nil
}

// copyFile 复制单个文件
func (u *Updater) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// 复制权限
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, srcInfo.Mode())
}
