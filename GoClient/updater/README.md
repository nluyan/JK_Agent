# Updater - 更新器程序

## 简介

这是一个独立的更新器程序，用于更新 JikeAgent 服务。它实现了与原 C# 版本相同的功能。

## 功能特性

- 停止 JikeAgent Windows 服务
- 从 `temp` 目录复制更新文件到主目录
- 递归复制所有文件和子目录
- 错误处理和日志记录
- 自动重启服务

## 工作流程

1. 启动时记录日志
2. 检查 `temp` 目录是否存在
3. 停止 JikeAgent 服务
4. 等待 5 秒确保服务完全停止
5. 从 `temp` 目录复制所有文件到主目录（覆盖模式）
6. 更新完成后启动 JikeAgent 服务

## 构建

### 使用构建脚本（推荐）

```powershell
.\build_updater.ps1
```

### 手动构建

```powershell
cd updater
go mod tidy
go build -ldflags="-s -w" -o Updater.exe .
```

## 使用方法

更新器会被主程序自动下载和启动，无需手动执行。

主程序的更新流程：
1. 下载 `Updater.exe` 到主目录
2. 下载 `AgentClient.zip` 更新包
3. 解压更新包到 `temp` 目录
4. 启动 `Updater.exe`
5. 主程序退出

## 日志

所有更新操作日志会记录到 `logs/update_log.txt` 文件。

日志格式：
```
2025-11-28 14:18:00 [INFO] 启动更新...
2025-11-28 14:18:00 [INFO] 停止服务: JikeAgent
2025-11-28 14:18:05 [INFO] 开始复制文件...
2025-11-28 14:18:10 [INFO] 更新完成
```

## 错误处理

- 如果找不到 `temp` 目录，会记录错误并退出
- 文件复制失败会记录错误但继续复制其他文件
- 无论更新成功与否，最后都会尝试启动服务

## 技术实现

- **语言**: Go 1.25.4
- **依赖**: golang.org/x/sys (Windows 服务管理)
- **平台**: Windows (使用 Windows Service API)

## 对照 C# 版本

与原 C# 版本的功能对应：

| 功能 | C# 实现 | Go 实现 |
|------|---------|---------|
| 服务管理 | ServiceHelper | golang.org/x/sys/windows/svc |
| 目录复制 | CopyDirectory | copyDirectory + copyRecursive |
| 文件复制 | File.Copy | io.Copy |
| 日志记录 | File.AppendAllText | appendLog |
| 等待延迟 | Thread.Sleep | time.Sleep |

## 注意事项

1. 必须以管理员权限运行（需要停止/启动服务）
2. 确保 `temp` 目录存在且包含更新文件
3. 更新过程中服务会短暂中断（约5-10秒）
