package agent

import (
	"archive/zip"
	"context"
	"encoding/json"
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
	enableDebug       bool
	checkInterval     time.Duration
	isCheckingUpdate  bool
	updateMutex       sync.Mutex
	stopChan          chan struct{}
	ctx               context.Context
	onCheckUpdateFunc func()
}


// NewUpdater 创建新的Updater实例
func NewUpdater(serverURL, group string, checkInterval time.Duration, logger zerolog.Logger, enableDebug bool) *Updater {
	if checkInterval < time.Minute {
		checkInterval = 10 * time.Minute // 默认10分钟
	}

	return &Updater{
		serverURL:     serverURL,
		group:         group,
		logger:        logger,
		enableDebug:   enableDebug,
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

	fmt.Printf("[AutoUpdate] 启动定期更新检查，间隔 %.1f 分钟\n", u.checkInterval.Minutes())
	if u.enableDebug {
		u.logger.Debug().
			Float64("intervalMinutes", u.checkInterval.Minutes()).
			Msg("启动定期更新检查")
	}

	// 启动定期检查协程
	go u.periodicCheck()
}


// Stop 停止定期更新检查
func (u *Updater) Stop() {
	close(u.stopChan)
}

// CheckNow 手动触发更新检查
func (u *Updater) CheckNow() {
	if u.enableDebug {
		u.logger.Debug().Msg("收到手动更新检查请求")
	}

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
			fmt.Println("[AutoUpdate] 开始执行一次定期更新检查")
			if err := u.checkAndUpdate(); err != nil {
				fmt.Printf("[AutoUpdate] 定期更新检查失败: %v\n", err)
				if u.enableDebug {
					u.logger.Debug().Err(err).Msg("定期更新检查失败")
				}
			}
		case <-u.stopChan:
			fmt.Println("[AutoUpdate] 定期更新检查已停止")
			if u.enableDebug {
				u.logger.Debug().Msg("定期更新检查已停止")
			}
			return
		case <-u.ctx.Done():
			fmt.Println("[AutoUpdate] 定期更新检查上下文取消")
			if u.enableDebug {
				u.logger.Debug().Msg("定期更新检查上下文取消")
			}
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

	fmt.Printf("[AutoUpdate] 开始执行定期版本检查，当前版本: %s\n", config.Default.Version)
	if u.enableDebug {
		u.logger.Debug().
			Str("currentVersion", config.Default.Version).
			Msg("开始执行定期版本检查")
	}

	if u.serverURL == "" {
		fmt.Println("[AutoUpdate] ServerURL 为空，跳过更新检查")
		if u.enableDebug {
			u.logger.Debug().Msg("ServerURL为空，跳过更新检查")
		}

		return nil
	}

	// 获取远程版本号

	remoteVersionURL := fmt.Sprintf("%s/update/%s/version.txt", u.serverURL, u.group)
	remoteVersion, err := u.fetchRemoteVersion(remoteVersionURL)
	if err != nil {
		return fmt.Errorf("获取远程版本失败: %w", err)
	}

	if remoteVersion == "" {
		fmt.Println("[AutoUpdate] 远程版本信息为空，跳过更新")
		if u.enableDebug {
			u.logger.Debug().Msg("远程版本信息为空，跳过更新")
		}
		return nil
	}

	// 比较版本
	if !u.isNewVersion(remoteVersion, config.Default.Version) {
		fmt.Printf("[AutoUpdate] 当前已是最新版本，远程版本: %s, 当前版本: %s\n", remoteVersion, config.Default.Version)
		if u.enableDebug {
			u.logger.Debug().
				Str("remoteVersion", remoteVersion).
				Str("currentVersion", config.Default.Version).
				Msg("定期检查完成，当前已是最新版本")
		}
		return nil
	}

	fmt.Printf("[AutoUpdate] 发现新版本，开始下载更新。远程版本: %s, 当前版本: %s\n", remoteVersion, config.Default.Version)
	if u.enableDebug {
		u.logger.Info().
			Str("remoteVersion", remoteVersion).
			Str("currentVersion", config.Default.Version).
			Msg("发现新版本，开始下载更新")
	}

	// 执行更新流程
	return u.performUpdate(remoteVersion)
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

	// 去掉首尾空白以及 UTF-8 BOM 等前导不可见字符
	version = strings.TrimSpace(version)
	version = strings.TrimLeftFunc(version, func(r rune) bool {
		return r < '0' || r > '9'
	})

	// 移除前缀 'v' 或 'V'
	version = strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")

	// 按点分割
	segments := strings.Split(version, ".")

	for i := 0; i < 3 && i < len(segments); i++ {
		// 解析数字部分（忽略非数字前后缀，如 "\ufeff2"、"1.0.0-beta"）
		numStr := segments[i]

		// 去掉前导非数字
		numStr = strings.TrimLeftFunc(numStr, func(r rune) bool {
			return r < '0' || r > '9'
		})

		// 截断第一个非数字之后
		for j, ch := range numStr {
			if ch < '0' || ch > '9' {
				numStr = numStr[:j]
				break
			}
		}

		if numStr == "" {
			continue
		}

		if num, err := strconv.Atoi(numStr); err == nil {
			parts[i] = num
		}
	}

	return parts
}


// performUpdate 执行更新流程
func (u *Updater) performUpdate(expectedVersion string) error {
	baseDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取工作目录失败: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows平台更新流程
		return u.performWindowsUpdate(baseDir, expectedVersion)
	}

	// Linux平台更新流程
	return u.performLinuxUpdate(baseDir, expectedVersion)
}


// performWindowsUpdate Windows平台更新
func (u *Updater) performWindowsUpdate(baseDir, expectedVersion string) error {
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

	// 校验更新包中的配置和版本
	if err := u.validatePackageVersion(tempDir, expectedVersion); err != nil {
		u.logger.Error().Err(err).Msg("更新包验证失败，取消本次更新")
		return err
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
func (u *Updater) performLinuxUpdate(baseDir, expectedVersion string) error {
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

	// 校验更新包中的配置和版本
	if err := u.validatePackageVersion(tempDir, expectedVersion); err != nil {
		u.logger.Error().Err(err).Msg("更新包验证失败，取消本次更新")
		return err
	}

	// 删除更新包
	u.logger.Info().Msg("删除更新包")
	if err := os.Remove(packagePath); err != nil {
		u.logger.Warn().Err(err).Msg("删除更新包失败")
	}

	// 复制除主二进制外的更新文件
	u.logger.Info().Msg("复制更新文件")
	if err := u.copyDirectory(tempDir, baseDir); err != nil {
		return fmt.Errorf("复制更新文件失败: %w", err)
	}

	// 使用 mv 语义替换主二进制 AgentClient
	tempExecPath := filepath.Join(tempDir, "AgentClient")
	execPath := filepath.Join(baseDir, "AgentClient")
	u.logger.Info().Msg("使用 mv 语义替换主二进制")
	if err := u.moveFile(tempExecPath, execPath); err != nil {
		return fmt.Errorf("替换主二进制失败: %w", err)
	}

	// 设置可执行权限
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
			if entry.Name() == "AgentClient" {
				// 主二进制由后续 mv 逻辑处理，这里跳过复制
				continue
			}
			// 复制文件
			if err := u.copyFile(srcPath, dstPath); err != nil {
				u.logger.Warn().Err(err).Str("file", srcPath).Msg("复制文件失败")
			}
		}
	}

	return nil
}


// moveDirectory 使用重命名方式移动目录内容，语义类似 mv
func (u *Updater) moveDirectory(src, dst string) error {
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

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.RemoveAll(dstPath); err != nil && !os.IsNotExist(err) {
				u.logger.Warn().Err(err).Str("dir", dstPath).Msg("删除旧目录失败")
			}
			if err := os.Rename(srcPath, dstPath); err != nil {
				u.logger.Warn().Err(err).Str("dir", srcPath).Msg("重命名目录失败，尝试递归移动")
				if err := u.moveDirectory(srcPath, dstPath); err != nil {
					return err
				}
			}
		} else {
			if err := u.moveFile(srcPath, dstPath); err != nil {
				u.logger.Warn().Err(err).Str("file", srcPath).Msg("移动文件失败")
			}
		}
	}

	return nil
}

// moveFile 使用重命名方式移动单个文件，必要时回退到复制
func (u *Updater) moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	if err := u.copyFile(src, dst); err != nil {
		return err
	}

	if err := os.Remove(src); err != nil {
		return err
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

type appSettingsConfig struct {
	Group       string `json:"Group"`
	ServerURL   string `json:"ServerUrl"`
	CheckUpdate int    `json:"CheckUpdate"`
}

// validatePackageVersion 校验更新包中的配置和目标版本
func (u *Updater) validatePackageVersion(tempDir, expectedVersion string) error {
	settingsPath := filepath.Join(tempDir, "appsettings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("读取更新包配置失败: %w", err)
	}

	var settings appSettingsConfig
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("解析更新包配置失败: %w", err)
	}

	settings.ServerURL = strings.TrimRight(strings.TrimSpace(settings.ServerURL), "/")
	settings.Group = strings.TrimSpace(settings.Group)

	if settings.ServerURL == "" || settings.Group == "" {
		return fmt.Errorf("更新包配置中的ServerUrl或Group为空")
	}

	versionURL := fmt.Sprintf("%s/update/%s/version.txt", settings.ServerURL, settings.Group)
	packageVersion, err := u.fetchRemoteVersion(versionURL)
	if err != nil {
		return fmt.Errorf("通过更新包配置检查版本失败: %w", err)
	}

	if strings.TrimSpace(packageVersion) != strings.TrimSpace(expectedVersion) {
		return fmt.Errorf("更新包版本(%s)与计划更新版本(%s)不一致", packageVersion, expectedVersion)
	}

	u.logger.Info().
		Str("version", packageVersion).
		Str("configServerUrl", settings.ServerURL).
		Str("configGroup", settings.Group).
		Msg("更新包appsettings.json验证通过")

	return nil
}

