package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Settings 应用配置
type Settings struct {
	Version     string
	ServerURL   string `json:"ServerUrl"`
	Group       string `json:"Group"`
	CheckUpdate int    `json:"CheckUpdate"` // 更新检查间隔（秒），0表示使用默认值10分钟
}

// Default 默认配置
var Default = Settings{
	Version: "1.0.0",
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
