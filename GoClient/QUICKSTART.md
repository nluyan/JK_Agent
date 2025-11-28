# 快速启动指南

## 1. 编译

### Windows
```powershell
go build -o JikeAgent.exe .
```

### Linux
```bash
go build -o JikeAgent .
```

### 交叉编译（可选）
```powershell
# 在 Windows 上编译 Linux 版本
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o JikeAgent-linux .

# 在 Linux 上编译 Windows 版本
GOOS=windows GOARCH=amd64 go build -o JikeAgent.exe .
```

## 2. 配置

### 方式一：环境变量

**Windows (PowerShell):**
```powershell
$env:AGENT_SERVER_URL = "http://your-server:5000/agenthub"
$env:AGENT_GROUP = "production"
```

**Linux/macOS (Bash):**
```bash
export AGENT_SERVER_URL="http://your-server:5000/agenthub"
export AGENT_GROUP="production"
```

### 方式二：配置文件（可选）

创建 `.env` 文件：
```bash
cp .env.example .env
# 编辑 .env 文件
```

## 3. 运行

### 直接运行
```bash
# Windows
.\JikeAgent.exe

# Linux
./JikeAgent
```

### 作为 Windows 服务

**安装：**
```powershell
# 以管理员身份运行 PowerShell
sc create JikeAgent binPath= "C:\path\to\JikeAgent.exe" start= auto
sc start JikeAgent
```

**查看状态：**
```powershell
sc query JikeAgent
```

**停止和删除：**
```powershell
sc stop JikeAgent
sc delete JikeAgent
```

### 作为 Linux systemd 服务

**一键安装：**
```bash
sudo ./JikeAgent --install
```

这会自动：
- 创建 systemd 服务文件
- 启用开机自启
- 立即启动服务

**管理命令：**
```bash
# 查看状态
sudo systemctl status jike.service

# 查看日志
sudo journalctl -u jike.service -f

# 停止服务
sudo systemctl stop jike.service

# 重启服务
sudo systemctl restart jike.service

# 禁用开机自启
sudo systemctl disable jike.service
```

**卸载：**
```bash
sudo systemctl stop jike.service
sudo systemctl disable jike.service
sudo rm /etc/systemd/system/jike.service
sudo systemctl daemon-reload
```

## 4. 验证

### 检查日志

**文件日志：**
```bash
# Windows
type logs\service-2025-11-28.log

# Linux
cat logs/service-2025-11-28.log
tail -f logs/service-2025-11-28.log
```

**systemd 日志（Linux）：**
```bash
sudo journalctl -u jike.service -n 50
sudo journalctl -u jike.service -f
```

### 预期日志内容

成功启动时应该看到：
```
{"level":"info","time":"...","message":"服务进入运行循环"}
{"level":"info","time":"...","message":"版本: 1.0.0"}
{"level":"info","time":"...","message":"MAC地址: E89C252B5C3C"}
{"level":"info","time":"...","message":"IP地址: 192.168.0.60"}
{"level":"debug","time":"...","message":"正在初始化SignalR连接..."}
{"level":"debug","time":"...","message":"尝试连接到服务器: http://..."}
{"level":"info","time":"...","message":"已连接到服务器..."}
{"level":"info","time":"...","message":"代理注册成功"}
{"level":"info","time":"...","message":"Agent服务已启动，持续运行中..."}
```

## 5. 测试功能

### 基础测试
```bash
go run test_basic.go
```

应该输出：
- MAC 地址
- IP 地址列表
- 平台信息
- PowerShell 测试结果

### PowerShell 脚本测试

服务器端调用示例（C# SignalR Hub）：
```csharp
// 获取系统信息
var script = "Get-ComputerInfo | Select-Object OsName, OsVersion | Format-List";
await Clients.Client(connectionId).SendAsync("ExecutePowershellScript", callId, script);
```

## 6. 常见问题

### 无法连接到服务器
1. 检查 `AGENT_SERVER_URL` 是否正确
2. 确认服务器正在运行
3. 检查防火墙设置
4. 查看日志中的详细错误

### PowerShell 脚本执行失败
1. 确认 PowerShell 已安装
2. Windows: 确认执行策略 `Get-ExecutionPolicy`
3. Linux: 确认 `pwsh` 已安装或在 `./pwsh/` 目录下

### 服务无法启动
1. Windows: 检查服务权限
2. Linux: 检查可执行权限 `chmod +x JikeAgent`
3. 确认工作目录存在且有写权限
4. 查看 systemd/Windows 服务日志

### MAC 地址显示 "none"
- 这是正常的，如果没有物理网卡（如虚拟机）
- 检查是否有活动的网络接口

## 7. 高级配置

### 自定义日志保留天数
编辑 `main.go`:
```go
const (
    logRetentionDays = 30  // 改为 30 天
)
```

### 修改版本号
编辑 `config/settings.go`:
```go
var Default = Settings{
    Version: "1.1.0",
}
```

### 添加自定义处理逻辑
编辑 `agent/agent.go`，在 `AgentReceiver` 中添加新方法：
```go
func (r *AgentReceiver) YourCustomMethod(param string) {
    r.agent.logger.Info().Msg("收到自定义方法调用")
    // 你的逻辑
}
```

## 8. 生产部署检查清单

- [ ] 已设置正确的 `AGENT_SERVER_URL`
- [ ] 已设置合适的 `AGENT_GROUP`
- [ ] 日志目录有写权限
- [ ] PowerShell 可用（Windows 内置 / Linux 需安装）
- [ ] 防火墙允许出站 HTTP/WebSocket 连接
- [ ] 服务配置为开机自启
- [ ] 已测试 PowerShell 脚本执行
- [ ] 已验证与服务器的连接
- [ ] 已设置日志监控或告警

## 9. 性能调优

### 内存优化
默认配置已经很轻量，如需进一步优化：
```bash
# 编译时优化
go build -ldflags="-s -w" -o JikeAgent .
```

### 减小可执行文件大小
使用 UPX 压缩：
```bash
upx --best --lzma JikeAgent.exe
```

## 10. 获取帮助

- 查看详细文档: `AGENT_README.md`
- 迁移总结: `MIGRATION_SUMMARY.md`
- 配置示例: `.env.example`

---

**就这么简单！现在你的 Agent 应该已经运行起来了。** 🎉
