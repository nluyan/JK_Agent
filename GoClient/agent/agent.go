package agent

import (
	"context"
	"fmt"
	"io"
	"jkagent/goclient/config"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/philippseith/signalr"

	"github.com/rs/zerolog"
)

// EventHandler 事件处理函数
type EventHandler func()

// Agent SignalR代理客户端
type Agent struct {
	serverURL         string
	group             string
	logger            zerolog.Logger
	client            signalr.Client
	onCheckUpdate     EventHandler
	stopChan          chan struct{}
	onRemoteDeskReady chan struct{}
}

// NewAgent 创建新的Agent实例
func NewAgent(serverURL, group string, logger zerolog.Logger) *Agent {
	return &Agent{
		serverURL:         serverURL,
		group:             group,
		logger:            logger,
		stopChan:          make(chan struct{}),
		onRemoteDeskReady: make(chan struct{}, 1),
	}
}

// SetOnCheckUpdate 设置CheckUpdate事件处理器
func (a *Agent) SetOnCheckUpdate(handler EventHandler) {
	a.onCheckUpdate = handler
}

// Start 启动Agent服务
func (a *Agent) Start(ctx context.Context) error {
	// 无限循环确保Agent永不退出
	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("Agent服务停止")
			return ctx.Err()
		case <-a.stopChan:
			a.logger.Info().Msg("Agent服务手动停止")
			return nil
		default:
		}

		if err := a.runOnce(ctx); err != nil {
			a.logger.Error().Err(err).Msg("Agent服务异常")
			a.logger.Warn().Msg("5秒后将重新启动Agent服务...")

			// 等待5秒后重试
			select {
			case <-time.After(5 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-a.stopChan:
				return nil
			}
		}
	}
}

// runOnce 运行一次连接循环
func (a *Agent) runOnce(ctx context.Context) error {
	a.logger.Debug().Msg("正在初始化SignalR连接...")

	// 检查服务器是否可连接
	a.logger.Debug().Msg("检查服务器是否可连接...")
	// 移除serverURL中的/AgentHub后缀用于版本检查
	serverBaseURL := a.serverURL
	if strings.HasSuffix(serverBaseURL, "/AgentHub") {
		serverBaseURL = strings.TrimSuffix(serverBaseURL, "/AgentHub")
	}
	versionURL := fmt.Sprintf("%s/update/%s/version.txt", serverBaseURL, a.group)
	if _, err := a.fetchRemoteVersion(versionURL); err != nil {
		return fmt.Errorf("服务器不可连接: %w", err)
	}
	a.logger.Debug().Msg("服务器连接检查成功")

	// 创建接收器
	receiver := &AgentReceiver{
		agent: a,
	}

	// 创建SignalR客户端
	client, err := signalr.NewClient(ctx,
		signalr.WithHttpConnection(ctx, a.serverURL, signalr.WithTransports(signalr.TransportWebSockets)),
		signalr.WithReceiver(receiver),
		signalr.TransferFormat(signalr.TransferFormatBinary), // 使用MessagePack，仅在 WebSockets 传输上使用
	)
	if err != nil {
		return fmt.Errorf("创建SignalR客户端失败: %w", err)
	}

	a.client = client

	// 监控连接状态
	stateChan := make(chan signalr.ClientState, 10)
	cancelObserve := client.ObserveStateChanged(stateChan)
	defer cancelObserve()

	// 状态监控协程
	go a.monitorState(ctx, stateChan)

	// 启动客户端
	client.Start()

	// 等待连接成功（仅用于第一次连接成功的错误检测，注册逻辑在 monitorState 中处理）
	a.logger.Debug().Msgf("尝试连接到服务器: %s", a.serverURL)
	if err := <-client.WaitForState(ctx, signalr.ClientConnected); err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}

	a.logger.Info().Msg("已连接到服务器...")
	a.logger.Info().Msg("Agent服务已启动，持续运行中...")

	// 启动定时更新goroutine
	updateCtx, updateCancel := context.WithCancel(ctx)
	go a.startPeriodicUpdate(updateCtx)
	defer updateCancel()

	// 等待连接断开或取消
	select {
	case <-ctx.Done():
		client.Stop()
		return ctx.Err()
	case <-a.stopChan:
		client.Stop()
		return nil
	}
}

// monitorState 监控连接状态
func (a *Agent) monitorState(ctx context.Context, stateChan <-chan signalr.ClientState) {
	for state := range stateChan {
		switch state {
		case signalr.ClientConnecting:
			a.logger.Debug().Msg("正在重新连接...")
		case signalr.ClientConnected:
			a.logger.Info().Msg("连接成功，开始注册代理...")
			if err := a.registerClient(ctx); err != nil {
				a.logger.Error().Err(err).Msg("注册代理失败")
			}
		case signalr.ClientClosed:
			a.logger.Warn().Msg("连接已关闭")
		}
	}
}

// registerClient 注册代理到服务器
func (a *Agent) registerClient(ctx context.Context) error {
	return a.updateAgentInternal(ctx, "RegisterAgent")
}

// updateAgent 更新代理信息到服务器
func (a *Agent) updateAgent(ctx context.Context) error {
	return a.updateAgentInternal(ctx, "UpdateAgent")
}

// updateAgentInternal 内部方法，用于注册或更新代理信息
func (a *Agent) updateAgentInternal(ctx context.Context, method string) error {
	platform := GetPlatform()
	osArch := GetOSArch()
	osDesc := GetOSDesc()
	macAddress := GetMacAddress()
	allIP := GetAllIP()
	hostName := GetHostName()

	if method == "RegisterAgent" {
		a.logger.Info().
			Str("mac", macAddress).
			Str("version", config.Default.Version).
			Str("ips", allIP).
			Str("group", a.group).
			Int("platform", platform).
			Str("arch", osArch).
			Str("os", osDesc).
			Str("hostname", hostName).
			Msg("注册代理信息")
	}

	// 调用服务器方法
	result := <-a.client.Invoke(method,
		macAddress,
		config.Default.Version,
		allIP,
		a.group,
		platform,
		osArch,
		osDesc,
		hostName,
	)

	if result.Error != nil {
		return result.Error
	}

	if method == "RegisterAgent" {
		a.logger.Info().Msg("代理注册成功")
	} else {
		a.logger.Debug().Msg("代理信息更新成功")
	}
	return nil
}

// Stop 停止Agent服务
func (a *Agent) Stop() {
	close(a.stopChan)
}

// startPeriodicUpdate 启动定期更新代理信息的任务
func (a *Agent) startPeriodicUpdate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	a.logger.Info().Msg("定期更新任务已启动，每1分钟更新一次代理信息")

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("定期更新任务停止（上下文取消）")
			return
		case <-a.stopChan:
			a.logger.Info().Msg("定期更新任务停止（服务停止）")
			return
		case <-ticker.C:
			// 调用UpdateAgent接口更新代理信息
			if err := a.updateAgent(ctx); err != nil {
				a.logger.Error().Err(err).Msg("更新代理信息失败")
			}
		}
	}
}

// AgentReceiver 接收服务器端调用
type AgentReceiver struct {
	agent *Agent
}

// CheckUpdate 服务器调用的检查更新方法
func (r *AgentReceiver) CheckUpdate() {
	defer func() {
		if err := recover(); err != nil {
			r.agent.logger.Error().Msgf("CheckUpdate发生panic: %v", err)
		}
	}()
	r.agent.logger.Info().Msg("收到CheckUpdate请求")
	if r.agent.onCheckUpdate != nil {
		r.agent.onCheckUpdate()
	}
}

// RemoteDesk 服务器调用的远程桌面方法
func (r *AgentReceiver) RemoteDesk(callID, server, key string) {
	r.agent.logger.Info().
		Str("callID", callID).
		Str("server", server).
		Str("key", key).
		Msg("收到RemoteDesk请求")

	// 启动远程桌面
	go func() {
		defer func() {
			if err := recover(); err != nil {
				r.agent.logger.Error().
					Str("callID", callID).
					Msgf("RemoteDesk发生panic: %v", err)
				// 发送错误回调
				result := fmt.Sprintf("内部错误: %v", err)
				if sendErr := <-r.agent.client.Send("RemoteDeskCallback", callID, result); sendErr != nil {
					r.agent.logger.Error().
						Err(sendErr).
						Str("callID", callID).
						Msg("发送RemoteDeskCallback失败")
				}
			}
		}()
		var result string

		// 检查平台支持
		if runtime.GOOS == "linux" {
			r.agent.logger.Warn().
				Str("callID", callID).
				Str("platform", runtime.GOOS).
				Msg("Linux平台不支持远程桌面功能")
			result = "错误：Linux平台不支持远程桌面功能"

			// 发送回调
			if err := <-r.agent.client.Send("RemoteDeskCallback", callID, result); err != nil {
				r.agent.logger.Error().
					Err(err).
					Str("callID", callID).
					Msg("发送RemoteDeskCallback失败")
			}
			return
		}

		// 调用 RustDesk IPC
		idAndPassword, err := StartRemoteDesk(server, key)
		if err != nil {
			r.agent.logger.Error().
				Err(err).
				Str("callID", callID).
				Msg("启动RemoteDesk失败")
			result = err.Error()
		} else {
			result = idAndPassword
			r.agent.logger.Info().
				Str("callID", callID).
				Str("result", result).
				Msg("RemoteDesk启动成功")
		}

		// 发送回调
		if err := <-r.agent.client.Send("RemoteDeskCallback", callID, result); err != nil {
			r.agent.logger.Error().
				Err(err).
				Str("callID", callID).
				Msg("发送RemoteDeskCallback失败")
		}
	}()
}

// ExecutePowershellScript 服务器调用的执行PowerShell脚本方法
func (r *AgentReceiver) ExecutePowershellScript(callID, script string) {
	r.agent.logger.Info().
		Str("callID", callID).
		Msg("收到ExecutePowershellScript请求")

	// 执行脚本
	go func() {
		defer func() {
			if err := recover(); err != nil {
				r.agent.logger.Error().
					Str("callID", callID).
					Msgf("ExecutePowershellScript发生panic: %v", err)
				// 发送错误回调
				outputText := fmt.Sprintf("执行脚本时发生内部错误: %v", err)
				if sendErr := <-r.agent.client.Send("PowershellScriptCallback", callID, outputText); sendErr != nil {
					r.agent.logger.Error().
						Err(sendErr).
						Str("callID", callID).
						Msg("发送PowershellScriptCallback失败")
				}
			}
		}()
		outputText := "执行结果为空。"

		r.agent.logger.Info().
			Str("callID", callID).
			Msgf("准备执行PowerShell脚本:\n%s", script)

		// 执行PowerShell脚本
		outputText = ExecuteScriptNatively(script)

		r.agent.logger.Info().
			Str("callID", callID).
			Msgf("PowerShell脚本执行结果:\n%s", outputText)

		// 发送回调
		if err := <-r.agent.client.Send("PowershellScriptCallback", callID, outputText); err != nil {
			r.agent.logger.Error().
				Err(err).
				Str("callID", callID).
				Msg("发送PowershellScriptCallback失败")
		} else {
			r.agent.logger.Info().
				Str("callID", callID).
				Msg("PowerShell脚本执行完成并已回调")
		}
	}()
}

// fetchRemoteVersion 获取远程版本号（用于检查服务器连接）
func (a *Agent) fetchRemoteVersion(url string) (string, error) {
	a.logger.Info().Str("url", url).Msg("正在检查服务器连通性，尝试获取版本文件")
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		a.logger.Error().Err(err).Str("url", url).Msg("服务器连接失败")
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		a.logger.Error().Int("statusCode", resp.StatusCode).Str("url", url).Msgf("服务器返回错误状态码: %d", resp.StatusCode)
		return "", fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logger.Error().Err(err).Str("url", url).Msg("读取服务器响应失败")
		return "", err
	}

	version := strings.TrimSpace(string(body))
	a.logger.Info().Str("url", url).Str("version", version).Msg("服务器连接检查成功，获取到版本信息")
	return version, nil
}
