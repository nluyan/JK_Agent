package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// WallpaperCommandPrefix 标记通过 ExecutePowershellScript 通道下发的壁纸命令。
// 与 SNMP 命令一样由 Go 客户端原生处理，不启动 PowerShell。
const WallpaperCommandPrefix = "__JK_AGENT_WALLPAPER__"

const (
	// maxWallpaperCommandBytes 限制命令体积；图片建议用 imageUrl 而不是 base64 内嵌。
	maxWallpaperCommandBytes = 12 * 1024 * 1024
	// maxWallpaperImageBytes 限制下载或解码后的图片体积。
	maxWallpaperImageBytes = 12 * 1024 * 1024
	// wallpaperDownloadTimeout 下载壁纸图片的超时时间。
	wallpaperDownloadTimeout = 30 * time.Second
	// wallpaperHelperTimeout 等待用户会话内的壁纸子进程退出的时间。
	// 它加上下载耗时仍要小于服务端为壁纸命令预留的 60 秒回调上限。
	wallpaperHelperTimeout = 20 * time.Second

	defaultWallpaperFileName = "wallpaper.jpg"
)

// wallpaperCommand 是服务端下发的壁纸命令。
type wallpaperCommand struct {
	// Operation: set(设置) / clear(恢复默认并解除锁定) / query(回读当前生效配置)
	Operation   string `json:"operation"`
	ImageBase64 string `json:"imageBase64,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	// Style 对应 WallpaperStyle：0=居中 2=拉伸 6=适应 10=填充 22=跨区
	Style *int `json:"style,omitempty"`
	Tile  bool `json:"tile,omitempty"`
	// Lock 为 true 时写入策略键，"设置→个性化"里的壁纸选项会变灰，用户改不掉。
	Lock *bool `json:"lock,omitempty"`
}

// isWallpaperCommand 判断脚本是否为壁纸命令。
func isWallpaperCommand(script string) bool {
	return strings.HasPrefix(strings.TrimSpace(script), WallpaperCommandPrefix)
}

// parseWallpaperCommand 解析壁纸命令载荷。
func parseWallpaperCommand(script string) (*wallpaperCommand, error) {
	payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), WallpaperCommandPrefix))
	if payload == "" {
		return nil, errors.New("壁纸命令内容为空")
	}
	if len(payload) > maxWallpaperCommandBytes {
		return nil, errors.New("壁纸命令体积超过限制")
	}
	command := &wallpaperCommand{}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(command); err != nil {
		return nil, fmt.Errorf("解析壁纸命令失败: %w", err)
	}
	command.Operation = strings.ToLower(strings.TrimSpace(command.Operation))
	if command.Operation == "" {
		command.Operation = "set"
	}
	return command, nil
}

// wallpaperStyle 返回平铺样式，未指定时用"填充"。
func (c *wallpaperCommand) wallpaperStyle() int {
	if c.Style == nil {
		return 10
	}
	return *c.Style
}

// wallpaperLock 返回是否锁定壁纸，未指定时默认锁定，符合"下发后用户改不掉"的运维预期。
func (c *wallpaperCommand) wallpaperLock() bool {
	if c.Lock == nil {
		return true
	}
	return *c.Lock
}

// safeWallpaperFileName 清洗文件名，避免 ../ 之类的路径穿越写到资源目录之外。
func safeWallpaperFileName(name string) string {
	candidate := strings.TrimSpace(name)
	if candidate == "" {
		return defaultWallpaperFileName
	}
	candidate = filepath.Base(filepath.FromSlash(candidate))
	candidate = strings.TrimLeft(candidate, ".")
	candidate = strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, candidate)
	if candidate == "" {
		return defaultWallpaperFileName
	}
	return candidate
}

// resolveWallpaperImage 取得壁纸图片字节：优先 base64，其次 URL 下载。
func resolveWallpaperImage(command *wallpaperCommand) ([]byte, error) {
	if encoded := strings.TrimSpace(command.ImageBase64); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(normalizeBase64(encoded))
		if err != nil {
			return nil, fmt.Errorf("解码base64图片失败: %w", err)
		}
		if len(decoded) == 0 {
			return nil, errors.New("base64图片内容为空")
		}
		if len(decoded) > maxWallpaperImageBytes {
			return nil, errors.New("图片体积超过限制")
		}
		return decoded, nil
	}

	imageURL := strings.TrimSpace(command.ImageURL)
	if imageURL == "" {
		return nil, errors.New("壁纸命令缺少 imageBase64 或 imageUrl")
	}
	client := &http.Client{Timeout: wallpaperDownloadTimeout}
	response, err := client.Get(imageURL)
	if err != nil {
		return nil, fmt.Errorf("下载壁纸失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载壁纸失败，HTTP状态码: %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxWallpaperImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取壁纸内容失败: %w", err)
	}
	if len(data) > maxWallpaperImageBytes {
		return nil, errors.New("壁纸体积超过限制")
	}
	if len(data) == 0 {
		return nil, errors.New("壁纸内容为空")
	}
	return data, nil
}

// normalizeBase64 去掉服务端可能加入的换行与 data URI 前缀。
func normalizeBase64(encoded string) string {
	if index := strings.Index(encoded, ","); index >= 0 && strings.HasPrefix(encoded, "data:") {
		encoded = encoded[index+1:]
	}
	replacer := strings.NewReplacer("\r", "", "\n", "", " ", "", "\t", "")
	return replacer.Replace(encoded)
}

// HandleWallpaperCommand 处理壁纸命令，返回给服务端的结果文本。
func HandleWallpaperCommand(script string) string {
	if !userSessionSupported() {
		return "壁纸功能仅支持 Windows 平台。"
	}
	command, err := parseWallpaperCommand(script)
	if err != nil {
		return err.Error()
	}

	switch command.Operation {
	case "set":
		return setWallpaper(command)
	case "clear":
		return clearWallpaper()
	case "query":
		return queryWallpaper()
	default:
		return fmt.Sprintf("不支持的壁纸操作: %s", command.Operation)
	}
}

// setWallpaper 把图片落到只读资源目录，再投递到用户会话生效。
// 壁纸必须以登录用户身份设置：SystemParametersInfo 与 HKCU 都只对调用者所在的用户生效，
// 以 SYSTEM 身份调用会写到 .DEFAULT 配置单元，用户桌面上看不到任何变化。
func setWallpaper(command *wallpaperCommand) string {
	image, err := resolveWallpaperImage(command)
	if err != nil {
		return err.Error()
	}

	session, err := ActiveConsoleSession()
	if err != nil {
		return err.Error()
	}

	assetsDir, err := AssetsDir()
	if err != nil {
		return err.Error()
	}
	imagePath := filepath.Join(assetsDir, safeWallpaperFileName(command.FileName))
	if err := writeFileAtomic(imagePath, image); err != nil {
		return "写入壁纸文件失败: " + err.Error()
	}

	result, err := runWallpaperHelper(session, wallpaperHelperTimeout,
		"-action", "set",
		"-image", imagePath,
		"-style", fmt.Sprintf("%d", command.wallpaperStyle()),
		"-tile", boolFlag(command.Tile),
		"-lock", boolFlag(command.wallpaperLock()))
	if err != nil {
		return err.Error()
	}
	return result
}

// clearWallpaper 恢复系统默认壁纸并解除策略锁定。
func clearWallpaper() string {
	session, err := ActiveConsoleSession()
	if err != nil {
		return err.Error()
	}
	result, err := runWallpaperHelper(session, wallpaperHelperTimeout, "-action", "clear")
	if err != nil {
		return err.Error()
	}
	return result
}

// queryWallpaper 回读该终端当前生效的壁纸配置，补偿服务端不存储策略带来的可见性缺失。
func queryWallpaper() string {
	session, err := ActiveConsoleSession()
	if err != nil {
		return err.Error()
	}
	result, err := runWallpaperHelper(session, wallpaperHelperTimeout, "-action", "query")
	if err != nil {
		return err.Error()
	}
	return result
}

// boolFlag 把布尔值转成子命令行使用的 0/1。
func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
