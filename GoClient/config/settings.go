package config

// Settings 应用配置
type Settings struct {
	Version string
}

// Default 默认配置
var Default = Settings{
	Version: "1.0.0",
}
