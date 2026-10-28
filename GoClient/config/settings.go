package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultStartupJitter 启动错峰上限（秒）：进程启动后随机延迟再连接服务器，避免大批Agent同时冲击服务端
	DefaultStartupJitter = 120
	// DefaultUpdateJitter 升级错峰上限（秒）：发现新版本后随机延迟再下载，避免同时拉取升级包打满服务端带宽
	DefaultUpdateJitter = 1800
)

// Settings 应用配置
type Settings struct {
	Version       string
	ServerURL     string `json:"ServerUrl"`
	Group         string `json:"Group"`
	CheckUpdate   int    `json:"CheckUpdate"`   // 更新检查间隔（秒），0表示使用默认值10分钟
	StartupJitter int    `json:"StartupJitter"` // 启动错峰上限（秒），0表示使用默认值，负数表示关闭错峰
	UpdateJitter  int    `json:"UpdateJitter"`  // 升级下载错峰上限（秒），0表示使用默认值，负数表示关闭错峰
	HubAuthToken  string `json:"HubAuthToken"`  // Hub鉴权令牌，需与服务端HubAuthToken一致；留空表示不携带
	Debug         bool   `json:"Debug"`
}

// Default 默认配置
var Default = Settings{
	Version: "2.23",
	Debug:   false,
}

// StartupJitterSeconds 返回启动错峰上限（秒），未配置时使用默认值
func (s *Settings) StartupJitterSeconds() int {
	return jitterSeconds(s.StartupJitter, DefaultStartupJitter)
}

// UpdateJitterSeconds 返回升级下载错峰上限（秒），未配置时使用默认值
func (s *Settings) UpdateJitterSeconds() int {
	return jitterSeconds(s.UpdateJitter, DefaultUpdateJitter)
}

// jitterSeconds 归一化错峰配置：负数表示关闭，0表示使用默认值
func jitterSeconds(value, fallback int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return fallback
	}
	return value
}

// LoadFromFile 从JSON配置文件加载配置
func LoadFromFile(configPath string) (*Settings, error) {
	if configPath == "" {
		// 使用默认路径
		baseDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("获取工作目录失败: %w", err)
		}
		configPath = filepath.Join(baseDir, "appsettings.json")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置版本号（从默认配置继承）
	settings.Version = Default.Version

	return &settings, nil
}
