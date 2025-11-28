//go:build !windows

package agent

import (
	"context"
	"errors"
)

const (
	pipeNameWindows = ""
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
	return "Not supported on this platform"
}

// IsConnected 是否已连接
func (s *ConnectionStatus) IsConnected() bool {
	return false
}

// HasActiveSessions 是否有活动会话
func (s *ConnectionStatus) HasActiveSessions() bool {
	return false
}

// OnlineStatus 在线状态
type OnlineStatus struct {
	Timestamp int64 `json:"timestamp"`
	IsOnline  bool  `json:"is_online"`
}

// RustDeskClient RustDesk IPC 客户端（存根实现）
type RustDeskClient struct {
	pipeName string
}

// NewRustDeskClient 创建 RustDesk 客户端
func NewRustDeskClient() *RustDeskClient {
	return &RustDeskClient{}
}

// SendRequest 发送请求并返回响应
func (c *RustDeskClient) SendRequest(ctx context.Context, request interface{}) (map[string]interface{}, error) {
	return nil, errors.New("RustDesk IPC only supported on Windows")
}

// SendRequestWithoutResponse 发送请求不等待响应
func (c *RustDeskClient) SendRequestWithoutResponse(ctx context.Context, request interface{}) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// GetConfigValue 获取配置值
func (c *RustDeskClient) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// SetConfigValue 设置配置值
func (c *RustDeskClient) SetConfigValue(ctx context.Context, key, value string) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// GetID 获取 RustDesk ID
func (c *RustDeskClient) GetID(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// GetTemporaryPassword 获取临时密码
func (c *RustDeskClient) GetTemporaryPassword(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// GetPermanentPassword 获取永久密码
func (c *RustDeskClient) GetPermanentPassword(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// SetPermanentPassword 设置永久密码
func (c *RustDeskClient) SetPermanentPassword(ctx context.Context, password string) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// SetTemporaryPassword 设置临时密码
func (c *RustDeskClient) SetTemporaryPassword(ctx context.Context, password string) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// GetFingerprint 获取指纹
func (c *RustDeskClient) GetFingerprint(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// GetRendezvousServer 获取中继服务器
func (c *RustDeskClient) GetRendezvousServer(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// GetOptions 获取选项
func (c *RustDeskClient) GetOptions(ctx context.Context) (map[string]string, error) {
	return nil, errors.New("RustDesk IPC only supported on Windows")
}

// SetOptions 设置选项
func (c *RustDeskClient) SetOptions(ctx context.Context, options map[string]string) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// GetConnectionStatus 获取连接状态
func (c *RustDeskClient) GetConnectionStatus(ctx context.Context) (*ConnectionStatus, error) {
	return nil, errors.New("RustDesk IPC only supported on Windows")
}

// IsConnected 检查是否已连接
func (c *RustDeskClient) IsConnected(ctx context.Context) (bool, error) {
	return false, errors.New("RustDesk IPC only supported on Windows")
}

// HasActiveSessions 检查是否有活动会话
func (c *RustDeskClient) HasActiveSessions(ctx context.Context) (bool, error) {
	return false, errors.New("RustDesk IPC only supported on Windows")
}

// GetSystemInfo 获取系统信息
func (c *RustDeskClient) GetSystemInfo(ctx context.Context) (string, error) {
	return "", errors.New("RustDesk IPC only supported on Windows")
}

// CloseRustDesk 关闭 RustDesk
func (c *RustDeskClient) CloseRustDesk(ctx context.Context) error {
	return errors.New("RustDesk IPC only supported on Windows")
}

// StartRemoteDesk 启动远程桌面
func StartRemoteDesk(server, key string) (string, error) {
	return "", errors.New("RustDesk only supported on Windows")
}

// fileExists 检查文件是否存在
func fileExists(path string) bool {
	return false
}
