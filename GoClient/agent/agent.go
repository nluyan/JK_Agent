package agent

import (
	"context"
	"fmt"
	"jkagent/goclient/config"
	"runtime"
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

	// 创建接收器
	receiver := &AgentReceiver{
		agent: a,
	}

	// 创建SignalR客户端
	client, err := signalr.NewClient(ctx,
		signalr.WithHttpConnection(ctx, a.serverURL),
		signalr.WithReceiver(receiver),
		signalr.TransferFormat("Binary"), // 使用MessagePack
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
	go a.monitorState(stateChan)

	// 启动客户端
	client.Start()

	// 等待连接成功
	a.logger.Debug().Msgf("尝试连接到服务器: %s", a.serverURL)
	if err := <-client.WaitForState(ctx, signalr.ClientConnected); err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}

	a.logger.Info().Msg("已连接到服务器...")

	// 注册代理
	if err := a.registerClient(ctx); err != nil {
		return fmt.Errorf("注册代理失败: %w", err)
	}

	a.logger.Info().Msg("Agent服务已启动，持续运行中...")

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
func (a *Agent) monitorState(stateChan <-chan signalr.ClientState) {
	for state := range stateChan {
		switch state {
		case signalr.ClientConnecting:
			a.logger.Debug().Msg("正在重新连接...")
		case signalr.ClientConnected:
			a.logger.Info().Msg("连接成功")
		case signalr.ClientClosed:
			a.logger.Warn().Msg("连接已关闭")
		}
	}
}

// registerClient 注册代理到服务器
func (a *Agent) registerClient(ctx context.Context) error {
	platform := GetPlatform()
	osArch := GetOSArch()
	osDesc := GetOSDesc()
	macAddress := GetMacAddress()
	allIP := GetAllIP()

	a.logger.Info().
		Str("mac", macAddress).
		Str("version", config.Default.Version).
		Str("ips", allIP).
		Str("group", a.group).
		Int("platform", platform).
		Str("arch", osArch).
		Str("os", osDesc).
		Msg("注册代理信息")

	// 调用服务器的RegisterAgent方法
	result := <-a.client.Invoke("RegisterAgent",
		macAddress,
		config.Default.Version,
		allIP,
		a.group,
		platform,
		osArch,
		osDesc,
	)

	if result.Error != nil {
		return result.Error
	}

	a.logger.Info().Msg("代理注册成功")
	return nil
}

// Stop 停止Agent服务
func (a *Agent) Stop() {
	close(a.stopChan)
}

// AgentReceiver 接收服务器端调用
type AgentReceiver struct {
	agent *Agent
}

// CheckUpdate 服务器调用的检查更新方法
func (r *AgentReceiver) CheckUpdate() {
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
		outputText := "执行结果为空。"

		r.agent.logger.Info().Msgf("执行脚本:\n%s", script)

		// 执行PowerShell脚本
		outputText = ExecuteScriptNatively(script)

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
