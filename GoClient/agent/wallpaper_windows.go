//go:build windows

package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	spiSetDeskWallpaper = 0x0014
	spiUpdateIniFile    = 0x01
	spiSendChange       = 0x02

	desktopRegistryPath = `Control Panel\Desktop`
	// wallpaperPolicyPath 是"强制壁纸"策略键。写入后"设置→个性化"里的壁纸选项变灰，
	// 用户无法自行更换，这是运维统一桌面的标准做法。
	wallpaperPolicyPath = `Software\Microsoft\Windows\CurrentVersion\Policies\System`
)

var (
	modUser32ForWallpaper     = syscall.NewLazyDLL("user32.dll")
	procSystemParametersInfoW = modUser32ForWallpaper.NewProc("SystemParametersInfoW")
)

// runWallpaperHelper 把壁纸设置子进程投递到登录用户的会话中执行。
// 必须使用用户令牌：SystemParametersInfo 与 HKCU 都只对调用进程所属的用户生效，
// 以 SYSTEM 身份调用会写到 .DEFAULT 配置单元，用户桌面上不会有任何变化。
func runWallpaperHelper(session UserSession, timeout time.Duration, args ...string) (string, error) {
	exePath, err := CurrentExecutablePath()
	if err != nil {
		return "", err
	}
	shareDir, err := UserShareDir()
	if err != nil {
		return "", err
	}
	resultPath := filepath.Join(shareDir, fmt.Sprintf("wallpaper_%d_%d.result.txt", time.Now().UnixNano(), rand.Int63()))
	defer os.Remove(resultPath)

	childArgs := append([]string{"--wallpaper"}, args...)
	childArgs = append(childArgs, "-"+childResultFlag, resultPath)

	process, err := LaunchInUserSession(session, TokenAsLoggedOnUser, exePath, childArgs, shareDir)
	if err != nil {
		return "", err
	}
	defer process.Close()

	if !process.Wait(timeout) {
		process.Terminate()
		return "", fmt.Errorf("在%s中设置壁纸超时(%d秒)，已终止", session.DisplayName(), int(timeout/time.Second))
	}

	output := strings.TrimSpace(decodeWithBomAndFallback(readFileIfExists(resultPath)))
	if output == "" {
		return "", fmt.Errorf("在%s中设置壁纸未返回结果", session.DisplayName())
	}
	return output, nil
}

// RunWallpaperChild 是 --wallpaper 子命令入口，由服务进程投递到用户会话中运行。
func RunWallpaperChild(args []string) int {
	values := parseChildFlags(args)
	resultPath := values[childResultFlag]

	var output string
	exitCode := 0
	switch strings.ToLower(strings.TrimSpace(values["action"])) {
	case "set":
		output = applyWallpaper(values)
	case "clear":
		output = resetWallpaper()
	case "query":
		output = describeWallpaper()
	default:
		output = fmt.Sprintf("未知的壁纸操作: %s", values["action"])
		exitCode = 1
	}

	writeResultFile(resultPath, output)
	return exitCode
}

// applyWallpaper 立即生效 + 写入用户配置持久化 + 按需锁定。
func applyWallpaper(values map[string]string) string {
	imagePath := strings.TrimSpace(values["image"])
	if imagePath == "" {
		return "缺少壁纸文件路径"
	}
	if _, err := os.Stat(imagePath); err != nil {
		return "壁纸文件不存在或不可读: " + imagePath
	}

	style := flagInt(values, "style", 10)
	tile := flagBool(values, "tile")
	lock := flagBool(values, "lock")

	var problems []string
	if err := setDeskWallpaper(imagePath); err != nil {
		problems = append(problems, "立即生效失败: "+err.Error())
	}
	if err := writeDesktopRegistry(imagePath, style, tile); err != nil {
		problems = append(problems, "写入用户配置失败: "+err.Error())
	}
	if lock {
		if err := writeWallpaperPolicy(imagePath, style); err != nil {
			problems = append(problems, "写入锁定策略失败: "+err.Error())
		}
	} else if err := clearWallpaperPolicy(); err != nil {
		problems = append(problems, "解除锁定失败: "+err.Error())
	}

	result := map[string]interface{}{
		"ok":      len(problems) == 0,
		"image":   imagePath,
		"style":   style,
		"tile":    tile,
		"lock":    lock,
		"session": currentSessionID(),
		"account": currentAccountName(),
	}
	if len(problems) > 0 {
		result["problems"] = problems
	}
	return marshalWallpaperResult(result)
}

// resetWallpaper 恢复系统默认壁纸并解除策略锁定。
func resetWallpaper() string {
	var problems []string
	if err := setDeskWallpaper(""); err != nil {
		problems = append(problems, "清除壁纸失败: "+err.Error())
	}
	if err := clearWallpaperPolicy(); err != nil {
		problems = append(problems, "解除锁定失败: "+err.Error())
	}
	result := map[string]interface{}{
		"ok":      len(problems) == 0,
		"cleared": true,
		"session": currentSessionID(),
		"account": currentAccountName(),
	}
	if len(problems) > 0 {
		result["problems"] = problems
	}
	return marshalWallpaperResult(result)
}

// describeWallpaper 回读当前用户实际生效的壁纸配置，供服务端 query 使用。
func describeWallpaper() string {
	result := map[string]interface{}{
		"session": currentSessionID(),
		"account": currentAccountName(),
	}
	desktopReadable := true
	if key, err := registry.OpenKey(registry.CURRENT_USER, desktopRegistryPath, registry.QUERY_VALUE); err == nil {
		defer key.Close()
		result["wallpaper"] = readRegistryString(key, "Wallpaper")
		result["style"] = readRegistryString(key, "WallpaperStyle")
		result["tile"] = readRegistryString(key, "TileWallpaper")
	} else {
		desktopReadable = false
		result["problem"] = "读取用户桌面配置失败: " + err.Error()
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, wallpaperPolicyPath, registry.QUERY_VALUE); err == nil {
		defer key.Close()
		policy := readRegistryString(key, "Wallpaper")
		result["locked"] = strings.TrimSpace(policy) != ""
		result["policyWallpaper"] = policy
	} else {
		result["locked"] = false
	}
	// 服务端按 success/ok 字段判定原生命令成败，query 也必须带上，否则会被误报"壁纸命令执行失败"。
	// 策略键不存在是常态（表示未锁定），不影响 ok。
	result["ok"] = desktopReadable
	return marshalWallpaperResult(result)
}

// marshalWallpaperResult 输出紧凑 JSON，方便服务端直接展示或解析。
func marshalWallpaperResult(result map[string]interface{}) string {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "序列化结果失败: " + err.Error()
	}
	return string(encoded)
}

// setDeskWallpaper 调用 SystemParametersInfo 让壁纸立即生效。
// imagePath 为空串表示恢复系统默认。
func setDeskWallpaper(imagePath string) error {
	pathPointer, err := windows.UTF16PtrFromString(imagePath)
	if err != nil {
		return err
	}
	ret, _, callErr := procSystemParametersInfoW.Call(
		uintptr(spiSetDeskWallpaper),
		0,
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(spiUpdateIniFile|spiSendChange),
	)
	if ret == 0 {
		return fmt.Errorf("SystemParametersInfo返回失败: %v", callErr)
	}
	return nil
}

// writeDesktopRegistry 写入用户桌面配置，保证重启后壁纸保留。
func writeDesktopRegistry(imagePath string, style int, tile bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, desktopRegistryPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetStringValue("Wallpaper", imagePath); err != nil {
		return err
	}
	if err := key.SetStringValue("WallpaperStyle", strconv.Itoa(style)); err != nil {
		return err
	}
	return key.SetStringValue("TileWallpaper", boolFlag(tile))
}

// writeWallpaperPolicy 写入强制壁纸策略，用户将无法自行更换壁纸。
func writeWallpaperPolicy(imagePath string, style int) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, wallpaperPolicyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue("Wallpaper", imagePath); err != nil {
		return err
	}
	return key.SetStringValue("WallpaperStyle", strconv.Itoa(style))
}

// clearWallpaperPolicy 移除强制壁纸策略，恢复用户自行更换壁纸的能力。
func clearWallpaperPolicy() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, wallpaperPolicyPath, registry.SET_VALUE)
	if err != nil {
		// 键不存在说明本来就没有锁定，视为成功。
		return nil
	}
	defer key.Close()
	_ = key.DeleteValue("Wallpaper")
	_ = key.DeleteValue("WallpaperStyle")
	return nil
}

// readRegistryString 读取字符串值，键不存在时返回空串。
func readRegistryString(key registry.Key, name string) string {
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return value
}

// currentSessionID 返回子进程所在的会话号，用于回传诊断信息。
func currentSessionID() uint32 {
	sessionID := uint32(0)
	_ = windows.ProcessIdToSessionId(uint32(os.Getpid()), &sessionID)
	return sessionID
}

// currentAccountName 返回子进程运行身份，正常情况下应当是登录用户而不是 SYSTEM。
func currentAccountName() string {
	return querySessionString(currentSessionID(), wtsInfoDomainName) + "\\" +
		querySessionString(currentSessionID(), wtsInfoUserName)
}
