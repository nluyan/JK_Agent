//go:build windows

package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/rs/zerolog/log"
)

const (
	// RustDesk 的默认 IPC 管道名称（Windows）
	pipeNameWindows = `\\.\pipe\RustDesk\query`
)

// ConnectionStatus 连接状态
type ConnectionStatus struct {
	StatusNum      int    `json:"status_num"`
	KeyConfirmed   bool   `json:"key_confirmed"`
	MouseTime      int64  `json:"mouse_time"`
	ID             string `json:"id"`
	VideoConnCount int    `json:"video_conn_count"`
}

// StatusDescription 获取状态描述
func (s *ConnectionStatus) StatusDescription() string {
	switch s.StatusNum {
	case -1:
		return "-1: Disconnected"
	case 0:
		return "0: Connected (Idle)"
	case 1:
		return "1: Connected (Active)"
	default:
		return fmt.Sprintf("Unknown Status (%d)", s.StatusNum)
	}
}

// IsConnected 是否已连接
func (s *ConnectionStatus) IsConnected() bool {
	return s.StatusNum >= 0
}

// HasActiveSessions 是否有活动会话
func (s *ConnectionStatus) HasActiveSessions() bool {
	return s.StatusNum > 0
}

// OnlineStatus 在线状态
type OnlineStatus struct {
	Timestamp int64 `json:"timestamp"`
	IsOnline  bool  `json:"is_online"`
}

// RustDeskClient RustDesk IPC 客户端
type RustDeskClient struct {
	pipeName string
	timeout  time.Duration
}

// NewRustDeskClient 创建 RustDesk 客户端
func NewRustDeskClient() *RustDeskClient {
	return &RustDeskClient{
		pipeName: pipeNameWindows,
		timeout:  2 * time.Second,
	}
}

// SendRequest 发送请求并返回响应
func (c *RustDeskClient) SendRequest(ctx context.Context, request interface{}) (map[string]interface{}, error) {
	// 连接到命名管道
	conn, err := winio.DialPipe(c.pipeName, &c.timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RustDesk IPC: %w", err)
	}
	defer conn.Close()

	// 设置超时
	deadline := time.Now().Add(c.timeout)
	conn.SetDeadline(deadline)

	// 序列化请求
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建长度前缀
	lengthPrefix := createLengthPrefix(len(requestJSON))

	// 写入请求
	if _, err := conn.Write(lengthPrefix); err != nil {
		return nil, fmt.Errorf("failed to write length prefix: %w", err)
	}
	if _, err := conn.Write(requestJSON); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// 读取响应头（第一个字节）
	firstByte := make([]byte, 1)
	if _, err := io.ReadFull(conn, firstByte); err != nil {
		return nil, fmt.Errorf("failed to read first byte: %w", err)
	}

	// 计算头部长度
	headerLen := (firstByte[0] & 0x3) + 1

	// 读取完整头部
	headerBuff := make([]byte, headerLen)
	headerBuff[0] = firstByte[0]
	if headerLen > 1 {
		if _, err := io.ReadFull(conn, headerBuff[1:]); err != nil {
			return nil, fmt.Errorf("failed to read header: %w", err)
		}
	}

	// 解析长度
	var rawLength uint32
	switch headerLen {
	case 1:
		rawLength = uint32(headerBuff[0])
	case 2:
		rawLength = uint32(binary.LittleEndian.Uint16(headerBuff))
	case 3:
		rawLength = uint32(headerBuff[0]) | uint32(headerBuff[1])<<8 | uint32(headerBuff[2])<<16
	case 4:
		rawLength = binary.LittleEndian.Uint32(headerBuff)
	}

	responseLength := int(rawLength >> 2)
	if responseLength == 0 {
		return nil, nil
	}

	// 读取响应体
	responseBuffer := make([]byte, responseLength)
	if _, err := io.ReadFull(conn, responseBuffer); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 解析 JSON
	var response map[string]interface{}
	if err := json.Unmarshal(responseBuffer, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return response, nil
}

// SendRequestWithoutResponse 发送请求不等待响应
func (c *RustDeskClient) SendRequestWithoutResponse(ctx context.Context, request interface{}) error {
	conn, err := winio.DialPipe(c.pipeName, &c.timeout)
	if err != nil {
		log.Error().Err(err).Msg("Connection to RustDesk IPC timed out")
		log.Error().Msg("Please ensure the main RustDesk application is running")
		return err
	}
	defer conn.Close()

	deadline := time.Now().Add(c.timeout)
	conn.SetDeadline(deadline)

	requestJSON, err := json.Marshal(request)
	if err != nil {
		return err
	}

	lengthPrefix := createLengthPrefix(len(requestJSON))

	if _, err := conn.Write(lengthPrefix); err != nil {
		return err
	}
	if _, err := conn.Write(requestJSON); err != nil {
		return err
	}

	return nil
}

// GetConfigValue 获取配置值
func (c *RustDeskClient) GetConfigValue(ctx context.Context, key string) (string, error) {
	request := map[string]interface{}{
		"t": "Config",
		"c": []interface{}{key, nil},
	}

	response, err := c.SendRequest(ctx, request)
	if err != nil {
		return "", err
	}

	if content, ok := response["c"].([]interface{}); ok && len(content) > 1 {
		if value, ok := content[1].(string); ok {
			return value, nil
		}
	}

	return "", nil
}

// SetConfigValue 设置配置值
func (c *RustDeskClient) SetConfigValue(ctx context.Context, key, value string) error {
	request := map[string]interface{}{
		"t": "Config",
		"c": []interface{}{key, value},
	}
	return c.SendRequestWithoutResponse(ctx, request)
}

// GetID 获取 RustDesk ID
func (c *RustDeskClient) GetID(ctx context.Context) (string, error) {
	return c.GetConfigValue(ctx, "id")
}

// GetTemporaryPassword 获取临时密码
func (c *RustDeskClient) GetTemporaryPassword(ctx context.Context) (string, error) {
	return c.GetConfigValue(ctx, "temporary-password")
}

// GetPermanentPassword 获取永久密码
func (c *RustDeskClient) GetPermanentPassword(ctx context.Context) (string, error) {
	return c.GetConfigValue(ctx, "permanent-password")
}

// SetPermanentPassword 设置永久密码
func (c *RustDeskClient) SetPermanentPassword(ctx context.Context, password string) error {
	return c.SetConfigValue(ctx, "permanent-password", password)
}

// SetTemporaryPassword 设置临时密码
func (c *RustDeskClient) SetTemporaryPassword(ctx context.Context, password string) error {
	return c.SetConfigValue(ctx, "temporary-password", password)
}

// GetFingerprint 获取指纹
func (c *RustDeskClient) GetFingerprint(ctx context.Context) (string, error) {
	return c.GetConfigValue(ctx, "fingerprint")
}

// GetRendezvousServer 获取中继服务器
func (c *RustDeskClient) GetRendezvousServer(ctx context.Context) (string, error) {
	return c.GetConfigValue(ctx, "rendezvous_server")
}

// GetOptions 获取选项
func (c *RustDeskClient) GetOptions(ctx context.Context) (map[string]string, error) {
	request := map[string]interface{}{
		"t": "Options",
	}

	response, err := c.SendRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	if content, ok := response["c"].(map[string]interface{}); ok {
		result := make(map[string]string)
		for k, v := range content {
			if str, ok := v.(string); ok {
				result[k] = str
			}
		}
		return result, nil
	}

	return nil, nil
}

// SetOptions 设置选项
func (c *RustDeskClient) SetOptions(ctx context.Context, options map[string]string) error {
	request := map[string]interface{}{
		"t": "Options",
		"c": options,
	}
	return c.SendRequestWithoutResponse(ctx, request)
}

// GetConnectionStatus 获取连接状态
func (c *RustDeskClient) GetConnectionStatus(ctx context.Context) (*ConnectionStatus, error) {
	log.Debug().Msg("发送连接获取在线状态")

	request := map[string]interface{}{
		"t": "OnlineStatus",
	}

	response, err := c.SendRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	content, ok := response["c"].([]interface{})
	if !ok || len(content) < 2 {
		return nil, fmt.Errorf("invalid response format: %v", response)
	}

	log.Debug().Msgf("收到返回值：%v", content)

	// 解析状态
	var statusNum int
	if timestamp, ok := content[0].(float64); ok {
		if timestamp > 0 {
			statusNum = 1
		} else if timestamp == 0 {
			statusNum = 0
		} else {
			statusNum = -1
		}
	}

	keyConfirmed := false
	if confirmed, ok := content[1].(bool); ok {
		keyConfirmed = confirmed
	}

	return &ConnectionStatus{
		StatusNum:    statusNum,
		KeyConfirmed: keyConfirmed,
	}, nil
}

// IsConnected 检查是否已连接
func (c *RustDeskClient) IsConnected(ctx context.Context) (bool, error) {
	status, err := c.GetConnectionStatus(ctx)
	if err != nil {
		return false, err
	}
	return status.IsConnected(), nil
}

// HasActiveSessions 检查是否有活动会话
func (c *RustDeskClient) HasActiveSessions(ctx context.Context) (bool, error) {
	status, err := c.GetConnectionStatus(ctx)
	if err != nil {
		return false, err
	}
	return status.HasActiveSessions(), nil
}

// GetSystemInfo 获取系统信息
func (c *RustDeskClient) GetSystemInfo(ctx context.Context) (string, error) {
	request := map[string]interface{}{
		"t": "SystemInfo",
	}

	response, err := c.SendRequest(ctx, request)
	if err != nil {
		return "", err
	}

	if content, ok := response["c"].(string); ok {
		return content, nil
	}

	return "", nil
}

// CloseRustDesk 关闭 RustDesk
func (c *RustDeskClient) CloseRustDesk(ctx context.Context) error {
	request := map[string]interface{}{
		"t": "Close",
	}
	return c.SendRequestWithoutResponse(ctx, request)
}

// createLengthPrefix 创建长度前缀（RustDesk 自定义协议）
func createLengthPrefix(length int) []byte {
	if length < 0 {
		panic("length cannot be negative")
	}

	if length <= 0x3F {
		return []byte{byte(length << 2)}
	}
	if length <= 0x3FFF {
		bytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(bytes, uint16((length<<2)|0x1))
		return bytes
	}
	if length <= 0x3FFFFF {
		header := uint32((length << 2) | 0x2)
		return []byte{byte(header), byte(header >> 8), byte(header >> 16)}
	}
	if length <= 0x3FFFFFFF {
		bytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(bytes, uint32((length<<2)|0x3))
		return bytes
	}

	panic("message length exceeds the maximum supported size")
}

// StartRemoteDesk 启动远程桌面（主要逻辑）
func StartRemoteDesk(server, key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	client := NewRustDeskClient()
	installCounter := false
	serverConfigured := false

	for {
		if err := ctx.Err(); err != nil {
			log.Error().Err(err).Msg("StartRemoteDesk 超时，未能获取 RustDesk ID 和密码")
			if serverConfigured {
				return "", errors.New("rustdesk服务器无法连接")
			}
			return "", fmt.Errorf("在限定时间内未能获取到 RustDesk ID 和密码: %w", err)
		}

		var status *ConnectionStatus
		var err error

		// 尝试获取连接状态
		status, err = client.GetConnectionStatus(ctx)
		if err != nil {
			// 检查 RustDesk 是否已安装
			programFiles := os.Getenv("ProgramFiles")
			programFilesX86 := os.Getenv("ProgramFiles(x86)")

			rustdeskPath1 := filepath.Join(programFiles, "RustDesk", "rustdesk.exe")
			rustdeskPath2 := filepath.Join(programFilesX86, "RustDesk", "rustdesk.exe")

			var rustdeskExe string
			if fileExists(rustdeskPath1) {
				rustdeskExe = rustdeskPath1
			} else if fileExists(rustdeskPath2) {
				rustdeskExe = rustdeskPath2
			}

			if rustdeskExe != "" {
				// 已安装但服务未运行，尝试安装服务
				log.Debug().Msgf("RustDesk已安装于: %s，尝试安装服务", rustdeskExe)
				exec.Command(rustdeskExe, "--install-service").Run()
				time.Sleep(5 * time.Second)
				continue
			}

			// 未安装，尝试安装
			if installCounter {
				return "", errors.New("RustDesk未安装，且自动安装失败")
			}

			installCounter = true
			log.Debug().Msg("开始安装rustdesk")

			cmd := exec.Command("rustdesk.exe", "--silent-install")
			if err := cmd.Run(); err != nil {
				log.Error().Err(err).Msg("安装RustDesk失败")
			}

			time.Sleep(10 * time.Second)
			continue
		}

		log.Debug().Msgf("获取到返回状态：%d %s", status.StatusNum, status.StatusDescription())

		if status.StatusNum > 0 {
			// 在线状态，检查服务器配置
			rustServer, err := client.GetRendezvousServer(ctx)
			if err != nil {
				log.Error().Err(err).Msg("获取服务器配置失败")
				time.Sleep(1 * time.Second)
				continue
			}

			if !strings.Contains(rustServer, server) {
				if !serverConfigured {
					// 需要设置服务器（仅设置一次）
					log.Debug().Msgf("开始设置服务器 %s %s", server, key)
					err := client.SetOptions(ctx, map[string]string{
						"custom-rendezvous-server": server,
						"key":                      key,
					})
					if err != nil {
						log.Error().Err(err).Msg("设置服务器失败")
					} else {
						serverConfigured = true
						log.Debug().Msg("服务器设置完成")
					}
				} else {
					log.Debug().Msg("服务器已配置过，等待 RustDesk 应用配置")
				}
				time.Sleep(3 * time.Second)
				continue
			}

			// 服务器配置正确，获取 ID 和密码
			log.Debug().Msg("开始获取Id和Password")
			id, err := client.GetID(ctx)
			if err != nil {
				log.Error().Err(err).Msg("获取ID失败")
				time.Sleep(1 * time.Second)
				continue
			}

			pwd, err := client.GetTemporaryPassword(ctx)
			if err != nil {
				log.Error().Err(err).Msg("获取密码失败")
				time.Sleep(1 * time.Second)
				continue
			}

			log.Debug().Msgf("获取到Id：%s Pwd:%s", id, pwd)
			return id + "," + pwd, nil

		} else if status.StatusNum <= 0 {
			// 离线或连接中，仅在未配置过时设置服务器
			if !serverConfigured {
				rustServer, err := client.GetRendezvousServer(ctx)
				if err != nil {
					log.Error().Err(err).Msg("获取服务器配置失败")
					time.Sleep(1 * time.Second)
					continue
				}

				if !strings.Contains(rustServer, server) {
					log.Debug().Msgf("开始设置服务器 %s %s", server, key)
					err := client.SetOptions(ctx, map[string]string{
						"custom-rendezvous-server": server,
						"key":                      key,
					})
					if err != nil {
						log.Error().Err(err).Msg("设置服务器失败")
					} else {
						serverConfigured = true
						log.Debug().Msg("服务器设置完成")
					}
				} else {
					serverConfigured = true
					log.Debug().Msg("服务器已配置为目标地址，无需重复设置")
				}
			} else {
				log.Debug().Msg("服务器已配置，等待 RustDesk 与服务器建立连接")
			}
			time.Sleep(3 * time.Second)
		} else {
			log.Debug().Msg("延迟一秒")
			time.Sleep(1 * time.Second)
		}

		// 如果状态为 nil，尝试启动 RustDesk
		if status == nil {
			log.Debug().Msg("启动rustdesk")
			cmd := exec.Command("RustDesk.exe", "--server")
			cmd.Dir = filepath.Dir(os.Args[0])
			if err := cmd.Start(); err != nil {
				log.Error().Err(err).Msg("启动RustDesk失败")
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// fileExists 检查文件是否存在
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
