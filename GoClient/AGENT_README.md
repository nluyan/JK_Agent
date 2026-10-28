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

#### 特殊 SNMP 操作命令

SNMP 操作继续复用 `ExecutePowershellScript` 和 `PowershellScriptCallback`。脚本内容以 `__JK_AGENT_SNMP__` 开头，后面紧跟一个 JSON 对象；Go 客户端会直接执行 SNMP 请求，不启动 PowerShell。

SNMP v2c GET 示例：

```text
__JK_AGENT_SNMP__
{"operation":"get","target":"192.168.1.10","version":"2c","community":"public","oids":["1.3.6.1.2.1.1.1.0","1.3.6.1.2.1.1.5.0"]}
```

WALK 示例：

```text
__JK_AGENT_SNMP__
{"operation":"walk","target":"192.168.1.10","version":"2c","community":"public","oid":"1.3.6.1.2.1.2.2","maxResults":5000}
```

SNMP v3 `authPriv` GET 示例：

```text
__JK_AGENT_SNMP__
{"operation":"get","target":"192.168.1.10","version":"3","username":"snmp-user","securityLevel":"authPriv","authProtocol":"sha256","authPassphrase":"auth-password","privProtocol":"aes128","privPassphrase":"priv-password","oids":["1.3.6.1.2.1.1.1.0"]}
```

SET 示例：

```text
__JK_AGENT_SNMP__
{"operation":"set","target":"192.168.1.10","version":"2c","community":"private","oid":"1.3.6.1.2.1.1.5.0","valueType":"string","value":"new-device-name"}
```

支持的操作：

- `get`
- `getnext`
- `getbulk`（SNMP v2c/v3）
- `walk`
- `set`

常用参数：

- `target`：设备 IP 或主机名，必填。
- `port`：默认 `161`。
- `transport`：`udp` 或 `tcp`，默认 `udp`。
- `version`：`1`、`2c` 或 `3`，默认 `2c`。
- `timeoutSeconds`：单次请求超时，默认 10 秒，最大 60 秒。
- `operationTimeoutSeconds`：整个操作超时，默认和最大均为 240 秒。
- `retries`：重试次数，范围 0–5。
- `maxRepetitions`：GETBULK/WALK 每次最大重复数，默认 25。
- `maxResults`：WALK 最大结果数，默认 10000，最大 50000。

SET 支持 `integer`、`string`/`octetString`、`base64`、`oid`、`gauge32`、`counter32`、`counter64`、`timeTicks` 和 `ipAddress`。返回值是 JSON，成功时 `success` 为 `true`；OctetString 同时提供 `valueBase64`，避免二进制数据丢失。

SNMP community、SNMPv3 认证密码和加密密码属于敏感信息，不要写入日志或长期保存到脚本历史中；生产环境优先使用 SNMPv3 `authPriv`。

#### 特殊水印命令

水印同样复用 `ExecutePowershellScript` 和 `PowershellScriptCallback`。脚本内容以 `__JK_AGENT_WATERMARK__` 开头，后面紧跟一个 JSON 对象；Go 客户端直接处理，不启动 PowerShell。

服务端不保存任何水印配置：文案随命令下发后由客户端持久化到 `%ProgramData%\JikeAgent\state\watermark.json`，服务重启或水印进程被结束后由客户端看门狗（5 秒一轮）自动恢复。

```text
__JK_AGENT_WATERMARK__
{"operation":"show","text":"{HostName} - {UserName} - 内部资料","opacity":28,"color":"#FFFFFF","fontSize":20,"angle":-30}
```

支持的操作：

- `show`：启用水印，必须提供 `text`，或终端已存有文案。
- `update`：只改样式，未提供的字段沿用终端本地已保存的值。
- `hide`：停用水印并清除本地策略。
- `query`：回读终端当前生效的水印状态。

参数：

- `text`：水印文案。
- `opacity`：不透明度百分比，0–100，默认 28。
- `color`：`#RRGGBB` 或 `#RGB`，默认 `#FFFFFF`，非法值回退为白色。
- `fontSize`：字号，8–200，默认 20。
- `angle`：倾斜角度，-90–90，默认 -30。
- `spacingX` / `spacingY`：平铺间距，0–10000，默认 0。

文案支持占位符。服务端替换 `{HostName}`、`{Serial}`、`{IP}`、`{Group}`、`{AgentId}`；客户端在每次投递时替换 `{UserName}`、`{SessionId}`、`{DateTime}`，因此换人登录后水印文案会跟着变。
水印渲染进程以 SYSTEM 令牌投递到登录用户的 `WinSta0\Default` 桌面，窗口带 `WS_EX_TOPMOST | WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE`，鼠标点击穿透且每秒重新置顶，每台显示器各一个窗口。进程属主是 SYSTEM，普通用户在任务管理器里结束它会得到“拒绝访问”；即使被管理员结束，看门狗也会在 5 秒内重新投递。

返回值是 JSON，成功时 `ok` 为 `true`。

#### 特殊壁纸命令

脚本内容以 `__JK_AGENT_WALLPAPER__` 开头，后面紧跟一个 JSON 对象，同样由 Go 客户端直接处理。

```text
__JK_AGENT_WALLPAPER__
{"operation":"set","imageUrl":"http://server/bg.jpg","style":10,"lock":true}
```

支持的操作：

- `set`：设置壁纸，图片来自 `imageBase64` 或 `imageUrl`，二选一，`imageBase64` 优先。
- `clear`：恢复系统默认壁纸并解除锁定。
- `query`：回读该终端当前生效的壁纸配置。

参数：

- `imageBase64`：Base64 图片，支持 `data:image/png;base64,` 前缀，解码后最大 12MB。
- `imageUrl`：图片下载地址，由客户端下载，超时 30 秒，最大 12MB。
- `fileName`：落盘文件名，客户端会剥掉目录部分并替换非法字符，默认 `wallpaper.jpg`。
- `style`：对应 `WallpaperStyle`，`0`=居中、`2`=拉伸、`6`=适应、`10`=填充（默认）、`22`=跨区。
- `tile`：是否平铺，默认 `false`。
- `lock`：是否锁定，默认 `true`。锁定后额外写入策略键，用户在“设置 → 个性化”里改不掉。
壁纸图片落在 `%ProgramData%\JikeAgent\assets`，子进程以登录用户令牌运行：`SystemParametersInfo` 与 `HKCU` 都只对调用进程所属的用户生效，以 SYSTEM 身份调用会写进 `.DEFAULT` 配置单元，用户桌面上不会有任何变化。代价是用户能在任务管理器里结束这个子进程，但它执行完就退出，不影响已生效的壁纸。

返回值是 JSON，成功时 `ok` 为 `true`，失败时 `problems` 列出具体原因。

#### 用户会话脚本指令

普通脚本在服务进程（Session 0）里执行，用户看不到任何界面。需要用户可见效果时，在脚本首行加上指令行：

```text
#JK-USERSESSION
Start-Process notepad
```

可选标志：

- `-System`：以 SYSTEM 身份运行，普通用户无法结束该进程。
- `-NoWait`：只确认进程已启动，不等待脚本执行完成。
- `-Session <id>`：指定会话，默认使用物理控制台会话。

客户端识别首行指令后改用 `CreateProcessAsUser` 把脚本投递到登录用户的交互式桌面执行。指令行之后必须有实际脚本内容，否则整段按普通脚本处理。

以 SYSTEM 身份运行时脚本只落在 SYSTEM 可写的 `tmp-system` 目录；用户令牌模式才使用 Users 可写的 `tmp-user`。SYSTEM 绝不从 `tmp-user` 加载脚本，否则普通用户替换文件内容即可把自己的代码提权到 SYSTEM 执行。

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
- `github.com/philippseith/signalr` - SignalR 客户端（本地分叉于 `signalr/`，详见「本地 signalr 依赖的分叉记录」）
- `github.com/rs/zerolog` - 结构化日志
- `github.com/kardianos/service` - 跨平台服务管理

## 本地 signalr 依赖的分叉记录

`signalr/` 与 `signalr/router/` 是 `github.com/philippseith/signalr` 的本地分叉，主模块通过 `replace github.com/philippseith/signalr => ./signalr` 引用。相对上游已有下列改动，重新同步上游时需逐条比对：

| 位置 | 改动 | 原因 |
|------|------|------|
| `signalr/*.go` | WebTransport / quic-go 相关代码已注释禁用 | Windows 7 兼容（既有改动，非本次引入） |
| `signalr/go.mod` | `gomega` v1.33.1、`ginkgo/v2` v2.17.2、`go-cmp` v0.6.0、`pprof` 2024 版、`x/crypto`~`x/tools` 一批钉在低版本 | 更高版本声明 `go 1.23`，会迫使本模块上调 `go 1.20`，进而破坏 `build_win7.ps1`（强制 `go1.20.14`） |
| `signalr/router/router_test/router_test.go:116` | `WaitForState(...)` 前补 `<-` | 该方法返回 `<-chan error` 而非 `error`，漏解引用会让 gomega 报 `Expected an error-type` |
| `signalr/chatsample/main.go:155`、`main_per_connection.go:114` | 补 `defer cancel()` | 消除 `lostcancel` vet 告警 |
| `signalr/signalr_test/server_test.go:83` | 改为 `exec.Command("node", "node_modules/jest/bin/jest.js", ...)` | 绕开 cmd.exe，见下文 |

`signalr/router` 未被主模块 import，也不在主模块 `go.mod` 中，其自身的 `go 1.23.0` 对 Win7 构建无影响。

### 禁止在 signalr/ 内执行 go mod tidy

`go mod tidy` 会把 `go 1.20` 升到 `go 1.23.0`，并连带删掉 `quic-go/quic-go`、`qpack`、`qtls-go1-20` 等 require——这些包的文件带 `go1.20` 构建约束，语言版本一升就静默失效。`go 1.20` 是 Win7 构建的硬约束，不能动。

调整依赖请改用 `go mod edit "-require=模块@版本"` 配合 `go mod download`；改完用 `go mod tidy "-go=1.20" -diff` 复核，该命令在保持 `go 1.20` 的前提下报告差异且不落地。注意 PowerShell 会拆分含 `@` 或 `.` 的参数，这类参数必须整体加引号。

### Windows 上 jest 的 ^ 转义问题

`server_test.go` 原先执行 `node_modules/.bin/jest` 并传 `--testNamePattern=^smoke`。Windows 上该路径解析到 `jest.cmd`，需经 cmd.exe 执行，而 `^` 是 cmd.exe 的转义字符，`^smoke` 被吃成 `smoke`：

- 锚定失效后，`smoke` 同时命中 `smoke test` 与 `MessagePack smoke test` 两个 describe
- 后者使用 MessagePack（二进制协议），而 `TestServerSmokeSSE` 起的是 SSE-only 服务端
- JS 客户端的 SSE 传输硬编码只支持 Text（见 `signalr_test/node_modules/@microsoft/signalr/dist/cjs/ServerSentEventsTransport.js:31`），于是必然报 `ServerSentEvents does not support Binary`

改用 `node` 直接执行 `node_modules/jest/bin/jest.js` 可绕开 cmd.exe，`^` 得以保留。Linux/macOS 上原本就不受此影响。

### 验证方式

三个模块各自独立，根目录的 `go test ./...` 不跨模块边界，需分别执行：

```
cd GoClient;                 go build ./...; go vet ./...; go test ./...
cd GoClient/signalr;         go build ./...; go vet ./...; go test ./...
cd GoClient/signalr/router;  go build ./...; go vet ./...; go test ./...
```

`signalr_test` 需要本机有 Node 与 npm（其 `TestMain` 会执行 `npm install`）。`signalr` 与主模块还应用 `go1.20.14` 复验 `build` 与 `vet`，以确保 Win7 构建路径未被破坏。不要在这些目录内运行 `gofmt`：vendored 文件为 CRLF，格式化会把整个模块转成 LF，产生大量无关改动。

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
