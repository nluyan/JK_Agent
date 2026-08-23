# JikeAgent - SignalR 代理客户端

这是从 C# 移植到 Go 的 SignalR 代理客户端，用于与 SignalR 服务器进行通信。

## 功能特性

- ✅ SignalR 客户端连接（支持 WebSocket 和 SSE）
- ✅ MessagePack 协议支持
- ✅ 自动重连机制
- ✅ 代理注册（MAC地址、IP地址、系统信息）
- ✅ 接收服务器端调用：
  - `CheckUpdate`: 检查更新通知
  - `ExecutePowershellScript`: 执行 PowerShell 脚本
  - `RemoteDesk`: 远程桌面控制（占位符）
- ✅ Windows 服务支持
- ✅ Linux systemd 服务支持
- ✅ 跨平台支持（Windows/Linux/macOS）

## 项目结构

```
.
├── agent/
│   ├── agent.go           # Agent 主逻辑
│   ├── system_info.go     # 系统信息收集
│   └── powershell.go      # PowerShell 脚本执行
├── config/
│   └── settings.go        # 配置管理
├── signalr/               # SignalR 库
└── main.go                # 服务入口
```

## 配置

通过环境变量配置：

- `AGENT_SERVER_URL`: SignalR 服务器地址（默认: `http://localhost:5000/agenthub`）
- `AGENT_GROUP`: 代理分组名称（默认: `default`）

### Windows 环境变量设置

```powershell
$env:AGENT_SERVER_URL = "http://your-server:5000/agenthub"
$env:AGENT_GROUP = "production"
```

### Linux 环境变量设置

```bash
export AGENT_SERVER_URL="http://your-server:5000/agenthub"
export AGENT_GROUP="production"
```

## 编译

```bash
go build -o JikeAgent.exe .
```

或使用构建脚本（Windows）：

```powershell
.\build.ps1
```

## 运行

### 直接运行

```bash
# Windows
JikeAgent.exe

# Linux
./JikeAgent
```

### 作为 Windows 服务安装

使用 kardianos/service 自动安装：

```powershell
# 以管理员身份运行
sc create JikeAgent binPath= "C:\path\to\JikeAgent.exe"
sc start JikeAgent

# 或使用 Windows 服务管理工具
```

### 作为 Linux systemd 服务安装

```bash
sudo ./JikeAgent --install
```

这会自动创建 systemd 服务文件并启动服务。

查看服务状态：
```bash
sudo systemctl status jike.service
```

查看日志：
```bash
sudo journalctl -u jike.service -f
```

停止服务：
```bash
sudo systemctl stop jike.service
```

卸载服务：
```bash
sudo systemctl stop jike.service
sudo systemctl disable jike.service
sudo rm /etc/systemd/system/jike.service
sudo systemctl daemon-reload
```

## 日志

日志文件存储在 `logs/` 目录下，按日期命名：
- 格式: `service-YYYY-MM-DD.log`
- 自动轮转，保留最近 15 天的日志

## 支持的服务器方法调用

### 1. CheckUpdate
服务器通知客户端检查更新。

```go
// 服务器端（C# SignalR Hub）
await Clients.Client(connectionId).SendAsync("CheckUpdate");
```

### 2. ExecutePowershellScript
执行 PowerShell 脚本并返回结果。

```go
// 服务器端（C# SignalR Hub）
await Clients.Client(connectionId).SendAsync("ExecutePowershellScript", callId, scriptContent);

// 客户端会通过 PowershellScriptCallback 返回结果
```

#### 特殊硬件采集命令

继续调用同一个 `ExecutePowershellScript` 方法时，如果脚本内容（去掉首尾空白后）严格等于：

```text
__JK_AGENT_COLLECT_HARDWARE_INFO__
```

Go 客户端会在 Windows 上直接通过 WMI 采集硬件信息，不启动 PowerShell，结果仍通过原有的 `PowershellScriptCallback` 返回。这样可以兼容 Windows 7 PowerShell 2.0。

脚本执行特性：
- 自动处理 UTF-8/UTF-16 编码
- 捕获标准输出和错误输出
- 支持 BOM 检测
- 跨平台 PowerShell 支持（Windows PowerShell/PowerShell Core）

### 3. RemoteDesk
远程桌面控制（需要实现 RustDesk IPC）。

```go
// 服务器端（C# SignalR Hub）
await Clients.Client(connectionId).SendAsync("RemoteDesk", callId, server, key);

// 客户端会通过 RemoteDeskCallback 返回结果
```

## 客户端注册信息

Agent 启动后会向服务器注册以下信息：

```go
RegisterAgent(
    macAddress,    // MAC 地址
    version,       // 版本号
    allIP,         // 所有 IPv4 地址（逗号分隔）
    group,         // 组名
    platform,      // 平台（1:Windows, 2:Linux, 3:macOS）
    osArch,        // 架构（amd64/arm64/etc）
    osDesc         // 操作系统描述
)
```

## 系统信息收集

- **MAC 地址**: 自动过滤虚拟网卡，获取物理网卡地址
- **IP 地址**: 收集所有非回环的 IPv4 地址
- **平台信息**: 自动检测操作系统和架构

## PowerShell 脚本执行

### 编码处理

脚本执行器支持多种编码格式：
1. UTF-8 with BOM
2. UTF-16 LE with BOM
3. UTF-16 BE with BOM
4. 自动检测 UTF-16（基于零字节启发式）
5. 回退到 UTF-8

### PowerShell 路径优先级

Windows:
1. `./pwsh/pwsh.exe`（本地 PowerShell Core）
2. `powershell`（系统 Windows PowerShell）

Linux:
1. `./pwsh/pwsh`（本地 PowerShell Core）
2. `pwsh`（系统 PowerShell Core）

## 与 C# 版本的差异

| 功能 | C# 版本 | Go 版本 | 说明 |
|------|---------|---------|------|
| SignalR 连接 | ✅ | ✅ | 完全兼容 |
| MessagePack | ✅ | ✅ | 完全兼容 |
| 自动重连 | ✅ | ✅ | 使用 exponential backoff |
| PowerShell 执行 | ✅ | ✅ | 完全兼容 |
| RustDesk IPC | ✅ | ⚠️ | 需要实现 |
| 服务安装 | ✅ | ✅ | 完全兼容 |

## 依赖项

主要依赖：
- `github.com/philippseith/signalr` - SignalR 客户端
- `github.com/rs/zerolog` - 结构化日志
- `github.com/kardianos/service` - 跨平台服务管理

## 开发

### 添加新的服务器方法处理

在 `agent/agent.go` 的 `AgentReceiver` 中添加新方法：

```go
// 新方法示例
func (r *AgentReceiver) YourNewMethod(param1 string, param2 int) {
    r.agent.logger.Info().
        Str("param1", param1).
        Int("param2", param2).
        Msg("收到 YourNewMethod 请求")
    
    // 处理逻辑...
}
```

### 调试

启用详细日志：

```go
// 在 main.go 中设置日志级别
logger := zerolog.New(logWriter).
    With().
    Timestamp().
    Logger().
    Level(zerolog.DebugLevel)
```

## 故障排除

### 连接失败

1. 检查服务器 URL 是否正确
2. 确认网络连接
3. 查看日志文件中的详细错误信息

### PowerShell 脚本执行失败

1. 确认 PowerShell 可执行文件路径
2. 检查脚本编码
3. 查看 `.err.txt` 临时文件（如果存在）

### 服务无法启动

1. Windows: 检查服务权限
2. Linux: 检查 systemd 日志 `journalctl -u jike.service`
3. 确认工作目录中有 logs 文件夹的写权限

## 版本历史

- v1.0.0: 初始版本，从 C# 移植完成

## 许可证

与原项目保持一致
