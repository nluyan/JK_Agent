package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// WatermarkCommandPrefix 标记通过 ExecutePowershellScript 通道下发的水印命令。
// 与 SNMP、硬件采集命令一样由 Go 客户端原生处理，不启动 PowerShell。
const WatermarkCommandPrefix = "__JK_AGENT_WATERMARK__"

const (
	// maxWatermarkCommandBytes 限制命令体积。
	maxWatermarkCommandBytes = 64 * 1024
	// watermarkFileName 是水印策略的本地持久化文件名。
	// 服务端不保存任何水印配置，终端是唯一的存储方，服务重启后据此恢复。
	watermarkFileName = "watermark.json"
	// watermarkPIDFileName 记录水印渲染进程的 PID 与会话号。
	watermarkPIDFileName = "watermark.pid.json"

	defaultWatermarkOpacity  = 28
	defaultWatermarkColor    = "#FFFFFF"
	defaultWatermarkFontSize = 20
	defaultWatermarkAngle    = -30

	// watermarkWatchdogInterval 是看门狗轮询间隔。水印被用户强制结束后，
	// 最多经过该时长就会被重新投递到用户桌面。
	watermarkWatchdogInterval = 5 * time.Second
	// watermarkRendererStartupGrace 是等待渲染进程建窗并回写结果的宽限时间。
	// 渲染进程是常驻的，不能等它退出。
	watermarkRendererStartupGrace = 4 * time.Second
)

// watermarkCommand 是服务端下发的水印命令。
type watermarkCommand struct {
	// Operation: show(启用) / update(改样式或文案) / hide(停用并清除) / query(回读终端状态)
	Operation string `json:"operation"`
	Text      string `json:"text,omitempty"`
	Opacity   *int   `json:"opacity,omitempty"`
	Color     string `json:"color,omitempty"`
	FontSize  *int   `json:"fontSize,omitempty"`
	Angle     *int   `json:"angle,omitempty"`
	SpacingX  *int   `json:"spacingX,omitempty"`
	SpacingY  *int   `json:"spacingY,omitempty"`
}

// watermarkPolicy 是持久化在终端本地的水印策略。
type watermarkPolicy struct {
	Enabled   bool   `json:"enabled"`
	Text      string `json:"text"`
	Opacity   int    `json:"opacity"`
	Color     string `json:"color"`
	FontSize  int    `json:"fontSize"`
	Angle     int    `json:"angle"`
	SpacingX  int    `json:"spacingX"`
	SpacingY  int    `json:"spacingY"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// defaultWatermarkPolicy 返回带默认样式的策略，文案由服务端按机器单独下发。
func defaultWatermarkPolicy() watermarkPolicy {
	return watermarkPolicy{
		Opacity:  defaultWatermarkOpacity,
		Color:    defaultWatermarkColor,
		FontSize: defaultWatermarkFontSize,
		Angle:    defaultWatermarkAngle,
	}
}

// isWatermarkCommand 判断脚本是否为水印命令。
func isWatermarkCommand(script string) bool {
	return strings.HasPrefix(strings.TrimSpace(script), WatermarkCommandPrefix)
}

// parseWatermarkCommand 解析水印命令载荷。
func parseWatermarkCommand(script string) (*watermarkCommand, error) {
	payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), WatermarkCommandPrefix))
	if payload == "" {
		return nil, errors.New("水印命令内容为空")
	}
	if len(payload) > maxWatermarkCommandBytes {
		return nil, errors.New("水印命令体积超过限制")
	}
	command := &watermarkCommand{}
	if err := json.Unmarshal([]byte(payload), command); err != nil {
		return nil, fmt.Errorf("解析水印命令失败: %w", err)
	}
	command.Operation = strings.ToLower(strings.TrimSpace(command.Operation))
	if command.Operation == "" {
		command.Operation = "show"
	}
	command.Text = strings.TrimSpace(command.Text)
	return command, nil
}

// watermarkStatePath 返回策略文件路径。它位于仅 SYSTEM 可写的目录，
// 普通用户读得到但改不了，因此无法通过篡改配置来关闭水印。
func watermarkStatePath() (string, error) {
	stateDir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, watermarkFileName), nil
}

// watermarkPIDFilePath 返回渲染进程 PID 文件路径。
func watermarkPIDFilePath() (string, error) {
	stateDir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, watermarkPIDFileName), nil
}

// loadWatermarkPolicy 读取本地策略；文件不存在时返回 exists=false。
// 文件损坏按"没有策略"处理，避免看门狗在解析上反复失败。
func loadWatermarkPolicy() (watermarkPolicy, bool, error) {
	path, err := watermarkStatePath()
	if err != nil {
		return watermarkPolicy{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return watermarkPolicy{}, false, nil
		}
		return watermarkPolicy{}, false, err
	}
	policy := defaultWatermarkPolicy()
	if err := json.Unmarshal(data, &policy); err != nil {
		return watermarkPolicy{}, false, nil
	}
	return policy, true, nil
}

// saveWatermarkPolicy 原子写入本地策略。
func saveWatermarkPolicy(policy watermarkPolicy) error {
	path, err := watermarkStatePath()
	if err != nil {
		return err
	}
	policy.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// HandleWatermarkCommand 处理水印命令，返回给服务端的结果文本。
func HandleWatermarkCommand(script string) string {
	if !userSessionSupported() {
		return exitUnsupported("水印功能")
	}
	command, err := parseWatermarkCommand(script)
	if err != nil {
		return err.Error()
	}

	switch command.Operation {
	case "show", "update":
		return applyWatermarkCommand(command)
	case "hide":
		return disableWatermark()
	case "query":
		return describeWatermark()
	default:
		return fmt.Sprintf("不支持的水印操作: %s", command.Operation)
	}
}

// applyWatermarkCommand 合并新样式到本地策略，落盘后立刻同步一次运行状态。
func applyWatermarkCommand(command *watermarkCommand) string {
	policy, _, err := loadWatermarkPolicy()
	if err != nil {
		return err.Error()
	}
	if !policy.Enabled && strings.TrimSpace(policy.Text) == "" {
		policy = defaultWatermarkPolicy()
	}

	if command.Text != "" {
		policy.Text = command.Text
	}
	if command.Opacity != nil {
		policy.Opacity = clampWatermarkValue(*command.Opacity, 0, 100)
	}
	if strings.TrimSpace(command.Color) != "" {
		policy.Color = strings.TrimSpace(command.Color)
	}
	if command.FontSize != nil {
		policy.FontSize = clampWatermarkValue(*command.FontSize, 8, 200)
	}
	if command.Angle != nil {
		policy.Angle = clampWatermarkValue(*command.Angle, -90, 90)
	}
	if command.SpacingX != nil {
		policy.SpacingX = clampWatermarkValue(*command.SpacingX, 0, 10000)
	}
	if command.SpacingY != nil {
		policy.SpacingY = clampWatermarkValue(*command.SpacingY, 0, 10000)
	}
	policy.Enabled = true

	if strings.TrimSpace(policy.Text) == "" {
		return marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": "水印文案为空，show 操作必须提供 text",
		})
	}
	if err := saveWatermarkPolicy(policy); err != nil {
		return "保存水印策略失败: " + err.Error()
	}

	status, err := SyncWatermark()
	if err != nil {
		result := map[string]interface{}{"ok": false, "problem": err.Error(), "saved": true}
		return marshalWatermarkResult(result)
	}
	status["ok"] = true
	status["saved"] = true
	status["text"] = policy.Text
	return marshalWatermarkResult(status)
}

// disableWatermark 停用水印并清除本地策略。
func disableWatermark() string {
	policy := defaultWatermarkPolicy()
	policy.Enabled = false
	if err := saveWatermarkPolicy(policy); err != nil {
		return "保存水印策略失败: " + err.Error()
	}
	stopped, err := stopWatermarkRenderer()
	if err != nil {
		return marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": err.Error(), "enabled": false,
		})
	}
	return marshalWatermarkResult(map[string]interface{}{
		"ok": true, "enabled": false, "stopped": stopped,
	})
}

// describeWatermark 回读终端当前生效的水印状态。服务端不保存策略，
// 运维只能靠这个查询确认某台机器上的水印是否还在、文案是什么。
func describeWatermark() string {
	policy, exists, err := loadWatermarkPolicy()
	if err != nil {
		return "读取水印策略失败: " + err.Error()
	}
	pid, sessionID, running := watermarkRendererStatus()
	result := map[string]interface{}{
		"ok":         true,
		"configured": exists,
		"enabled":    exists && policy.Enabled,
		"text":       policy.Text,
		"opacity":    policy.Opacity,
		"color":      policy.Color,
		"fontSize":   policy.FontSize,
		"angle":      policy.Angle,
		"updatedAt":  policy.UpdatedAt,
		"running":    running,
		"pid":        pid,
		"session":    sessionID,
		"watchdog":   int(watermarkWatchdogInterval / time.Second),
	}
	return marshalWatermarkResult(result)
}

// SyncWatermark 让水印的实际运行状态与本地策略保持一致。
// 命令下发与看门狗轮询都调用它，逻辑只有一份。
func SyncWatermark() (map[string]interface{}, error) {
	if !userSessionSupported() {
		return nil, errUserSessionUnsupported
	}

	policy, exists, err := loadWatermarkPolicy()
	if err != nil {
		return nil, err
	}
	if !exists || !policy.Enabled || strings.TrimSpace(policy.Text) == "" {
		stopped, stopErr := stopWatermarkRenderer()
		if stopErr != nil {
			return nil, stopErr
		}
		return map[string]interface{}{"enabled": false, "running": false, "stopped": stopped}, nil
	}

	session, err := ActiveConsoleSession()
	if err != nil {
		// 没有用户登录时无从显示，保留策略不动，等下一次轮询再投递。
		return nil, err
	}

	pid, sessionID, running := watermarkRendererStatus()
	if running && pid > 0 && sessionID == session.SessionID {
		return map[string]interface{}{
			"enabled": true, "running": true, "pid": pid, "session": sessionID,
		}, nil
	}

	// 进程已死、或控制台会话切换到了另一个用户：先清理残留再重新投递。
	if _, stopErr := stopWatermarkRenderer(); stopErr != nil {
		return nil, stopErr
	}
	newPID, err := startWatermarkRenderer(policy, session)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"enabled": true, "running": true, "pid": newPID, "session": session.SessionID,
		"account": session.AccountName(),
	}, nil
}

// StartWatermarkWatchdog 启动看门狗协程。
// 服务端不保存策略，终端是唯一的事实来源，因此必须由本地看门狗保证水印常驻：
// 进程被用户强制结束后自动复活，用户登录或切换会话后自动补投，服务重启后按本地策略恢复。
func StartWatermarkWatchdog(ctx context.Context, logger zerolog.Logger) {
	if !userSessionSupported() {
		return
	}
	go func() {
		// 服务启动后立刻同步一次，恢复上次下发的水印。
		syncWatermarkOnce(logger)
		ticker := time.NewTicker(watermarkWatchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("水印看门狗已停止")
				return
			case <-ticker.C:
				syncWatermarkOnce(logger)
			}
		}
	}()
}

// syncWatermarkOnce 执行一次同步，并把"没有用户登录"降级为调试日志，避免刷屏。
func syncWatermarkOnce(logger zerolog.Logger) {
	status, err := SyncWatermark()
	if err != nil {
		if errors.Is(err, errNoLoggedOnUser) {
			logger.Debug().Msg("水印看门狗: 当前没有已登录的用户会话")
			return
		}
		logger.Warn().Err(err).Msg("水印看门狗同步失败")
		return
	}
	if running, _ := status["running"].(bool); running {
		logger.Debug().Interface("status", status).Msg("水印状态正常")
	}
}

// clampWatermarkValue 把数值限制在合理区间内，避免服务端下发异常值导致渲染失败。
func clampWatermarkValue(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// marshalWatermarkResult 输出紧凑 JSON 结果。
func marshalWatermarkResult(result map[string]interface{}) string {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "序列化水印结果失败: " + err.Error()
	}
	return string(encoded)
}

// parseHexColor 解析 #RRGGBB 形式的颜色，非法时回退到白色。
func parseHexColor(color string) (byte, byte, byte) {
	value := strings.TrimSpace(color)
	value = strings.TrimPrefix(value, "#")
	if len(value) == 3 {
		value = string([]byte{value[0], value[0], value[1], value[1], value[2], value[2]})
	}
	if len(value) != 6 {
		return 0xFF, 0xFF, 0xFF
	}
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return 0xFF, 0xFF, 0xFF
	}
	return byte(parsed >> 16), byte(parsed >> 8), byte(parsed)
}

// substituteWatermarkPlaceholders 替换只有终端才知道的占位符。
// {UserName} 必须在每次投递时替换，因为换人登录后水印要跟着变成新的登录用户。
func substituteWatermarkPlaceholders(text string, account string, sessionID uint32, now time.Time) string {
	replacer := strings.NewReplacer(
		"{UserName}", account,
		"{SessionId}", strconv.FormatUint(uint64(sessionID), 10),
		"{DateTime}", now.Format("2006-01-02 15:04"),
	)
	return replacer.Replace(text)
}
