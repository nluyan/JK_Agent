package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"jkagent/goclient/config"
	"math/rand"
	"net/http"
	"net/url"
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
	serverURLs        []string // 服务器URL列表（分号分隔解析）
	currentURLIndex   int      // 当前使用的URL索引
	group             string
	logger            zerolog.Logger
	client            signalr.Client
	onCheckUpdate     EventHandler
	stopChan          chan struct{}
	onRemoteDeskReady chan struct{}
	startupJitter     time.Duration // 启动错峰上限，实际延迟在 0 到该值之间随机取值
	hubToken          string        // Hub鉴权令牌，需与服务端HubAuthToken一致；为空表示不携带
}

// NewAgent 创建新的Agent实例
func NewAgent(serverURL, group string, logger zerolog.Logger) *Agent {
	// 解析分号分隔的服务器URL列表
	var serverURLs []string
	if strings.Contains(serverURL, ";") {
		for _, url := range strings.Split(serverURL, ";") {
			url = strings.TrimSpace(url)
			if url != "" {
				serverURLs = append(serverURLs, url)
			}
		}
		logger.Info().Strs("urls", serverURLs).Msg("解析到多个服务器URL")
	} else {
		// 单个URL情况
		serverURLs = []string{serverURL}
		logger.Info().Str("url", serverURL).Msg("使用单个服务器URL")
	}

	return &Agent{
		serverURLs:        serverURLs,
		currentURLIndex:   0,
		group:             group,
		logger:            logger,
		stopChan:          make(chan struct{}),
		onRemoteDeskReady: make(chan struct{}, 1),
	}
}

// getCurrentServerURL 获取当前使用的完整服务器URL（包含/AgentHub后缀）
func (a *Agent) getCurrentServerURL() string {
	url := a.serverURLs[a.currentURLIndex]
	if !strings.HasSuffix(url, "/AgentHub") {
		url += "/AgentHub"
	}
	return url
}

// getCurrentBaseURL 获取当前使用的基础URL（不包含/AgentHub后缀，用于版本检查）
func (a *Agent) getCurrentBaseURL() string {
	baseURL := a.serverURLs[a.currentURLIndex]
	if strings.HasSuffix(baseURL, "/AgentHub") {
		baseURL = strings.TrimSuffix(baseURL, "/AgentHub")
	}
	return baseURL
}

// getNextServerURL 获取下一个要尝试的服务器URL
func (a *Agent) getNextServerURL() {
	// 循环到下一个URL索引
	a.currentURLIndex = (a.currentURLIndex + 1) % len(a.serverURLs)
	a.logger.Info().Str("nextUrl", a.getCurrentServerURL()).Msg("切换到下一个服务器URL")
}

// SetOnCheckUpdate 设置CheckUpdate事件处理器
func (a *Agent) SetOnCheckUpdate(handler EventHandler) {
	a.onCheckUpdate = handler
}

// SetHubToken 设置Hub鉴权令牌；服务端配置了HubAuthToken时必须设置，否则连接会被拒绝
func (a *Agent) SetHubToken(token string) {
	a.hubToken = token
}

// getHubConnectionURL 返回带鉴权令牌的Hub地址；未配置令牌时与getCurrentServerURL一致。
// 令牌只在这里拼接，日志一律使用getCurrentServerURL，避免凭据落进日志。
func (a *Agent) getHubConnectionURL() string {
	hubURL := a.getCurrentServerURL()
	if a.hubToken == "" {
		return hubURL
	}
	separator := "?"
	if strings.Contains(hubURL, "?") {
		separator = "&"
	}
	return hubURL + separator + "access_token=" + url.QueryEscape(a.hubToken)
}

// SetStartupJitter 设置启动错峰上限，实际启动延迟在 0 到该值之间随机取值
func (a *Agent) SetStartupJitter(jitter time.Duration) {
	if jitter < 0 {
		jitter = 0
	}
	a.startupJitter = jitter
}

// startupDelay 计算本次启动的错峰延迟
func (a *Agent) startupDelay() time.Duration {
	if a.startupJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(a.startupJitter) + 1))
}

const (
	// initialRetryDelay 连接失败后的首次重试间隔
	initialRetryDelay = 5 * time.Second
	// maxRetryDelay 连接失败后的最大重试间隔
	maxRetryDelay = 5 * time.Minute
	// agentUpdateInterval 定期上报Agent信息的基准间隔
	agentUpdateInterval = 1 * time.Minute
	// stableConnectionDuration 连接保持超过该时长即视为服务端可达，重连时重置退避间隔
	stableConnectionDuration = 1 * time.Minute
)

// errConnectionClosed 连接建立成功后被关闭，外层循环据此重建客户端
var errConnectionClosed = errors.New("SignalR连接已关闭，准备重新连接")

// nextRetryDelay 按指数退避计算下一次重试间隔，上限为 maxRetryDelay
func nextRetryDelay(current time.Duration) time.Duration {
	next := current * 2
	if next <= 0 || next > maxRetryDelay {
		return maxRetryDelay
	}
	return next
}

// shouldResetRetryDelay 判断连接中断后是否应把退避间隔重置回初始值：
// 连接曾建立并稳定保持一段时间，说明服务端可达，应尽快重连；
// 刚连上就断开的抖动场景继续退避，避免大批Agent高频重连冲击服务端。
func shouldResetRetryDelay(err error, uptime time.Duration) bool {
	return errors.Is(err, errConnectionClosed) && uptime >= stableConnectionDuration
}

// jitterDuration 在基准时长上叠加 ±30% 抖动，避免大批Agent同时重连或同时发起请求
func jitterDuration(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	span := int64(base) * 3 / 5
	return time.Duration(int64(base) + rand.Int63n(span+1) - span/2)
}

// Start 启动Agent服务
func (a *Agent) Start(ctx context.Context) error {
	// 启动错峰：随机延迟后再连接，避免大批Agent同时冲击服务端
	if delay := a.startupDelay(); delay > 0 {
		a.logger.Info().Msgf("启动错峰，%s后连接服务器", delay.Round(time.Second))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			a.logger.Info().Msg("Agent服务停止")
			return ctx.Err()
		case <-a.stopChan:
			a.logger.Info().Msg("Agent服务手动停止")
			return nil
		}
	}

	// SignalR库内部的退避重连在持续失败约15分钟后会放弃（状态转为ClientClosed），
	// 由下面的外层循环负责重建客户端，保证Agent永不退出。
	retryDelay := initialRetryDelay

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

		startedAt := time.Now()
		if err := a.runOnce(ctx); err != nil {
			a.logger.Error().Err(err).Msg("Agent服务异常")

			// 连接曾稳定保持过说明服务端可达，重置退避间隔，
			// 避免长期运行后重连间隔一直停留在最大值导致恢复缓慢
			if shouldResetRetryDelay(err, time.Since(startedAt)) {
				retryDelay = initialRetryDelay
			}

			// 指数退避 + 抖动后重试，避免大批Agent同时重连
			delay := jitterDuration(retryDelay)
			a.logger.Warn().Msgf("%s后将重新启动Agent服务...", delay.Round(time.Second))

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			case <-a.stopChan:
				return nil
			}
			retryDelay = nextRetryDelay(retryDelay)
			continue
		}

		retryDelay = initialRetryDelay
	}
}

// runOnce 运行一次连接循环
func (a *Agent) runOnce(ctx context.Context) error {
	a.logger.Debug().Msg("正在初始化SignalR连接...")

	// 检查服务器是否可连接
	a.logger.Debug().Msg("检查服务器是否可连接...")
	// 使用当前URL进行版本检查
	versionURL := fmt.Sprintf("%s/update/%s/version.txt", a.getCurrentBaseURL(), a.group)
	if _, err := a.fetchRemoteVersion(versionURL); err != nil {
		// 服务器连接失败，切换到下一个URL
		a.getNextServerURL()
		return fmt.Errorf("服务器不可连接: %w", err)
	}
	a.logger.Debug().Msg("服务器连接检查成功")

	// 创建接收器
	receiver := &AgentReceiver{
		agent: a,
	}

	// 创建SignalR客户端
	client, err := signalr.NewClient(ctx,
		signalr.WithHttpConnection(ctx, a.getHubConnectionURL(), signalr.WithTransports(signalr.TransportWebSockets)),
		signalr.WithReceiver(receiver),
		signalr.TransferFormat(signalr.TransferFormatBinary), // 使用MessagePack，仅在 WebSockets 传输上使用
	)
	if err != nil {
		// 创建客户端失败，切换到下一个URL
		a.getNextServerURL()
		return fmt.Errorf("创建SignalR客户端失败: %w", err)
	}

	a.client = client

	// 监控连接状态
	stateChan := make(chan signalr.ClientState, 10)
	cancelObserve := client.ObserveStateChanged(stateChan)
	defer cancelObserve()

	// 连接彻底关闭时由该通道通知，避免runOnce一直阻塞导致Agent假死
	connClosed := make(chan struct{})

	// 状态监控协程
	go a.monitorState(ctx, stateChan, connClosed)

	// 启动客户端
	client.Start()

	// 等待连接成功（仅用于第一次连接成功的错误检测，注册逻辑在 monitorState 中处理）
	a.logger.Debug().Msgf("尝试连接到服务器: %s", a.getCurrentServerURL())
	if err := <-client.WaitForState(ctx, signalr.ClientConnected); err != nil {
		// 连接失败，切换到下一个URL
		a.getNextServerURL()
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
	case <-a.stopChan:
		client.Stop()
		return nil
	case <-ctx.Done():
		client.Stop()
		return ctx.Err()
	case <-connClosed:
		client.Stop()
		// 正常停止时同样会收到连接关闭事件，不要当成异常处理
		select {
		case <-a.stopChan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errConnectionClosed
		}
	}
}

// monitorState 监控连接状态，连接彻底关闭时通过 connClosed 通知调用方重建连接
func (a *Agent) monitorState(ctx context.Context, stateChan <-chan signalr.ClientState, connClosed chan<- struct{}) {
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
			// 通知 runOnce 连接已彻底关闭，由外层循环重建客户端
			close(connClosed)
			return
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
	a.logger.Info().Msg("定期更新任务已启动，每1分钟更新一次代理信息")

	for {
		// 间隔叠加抖动，避免大批Agent同时上报形成周期性尖峰
		timer := time.NewTimer(jitterDuration(agentUpdateInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
			a.logger.Info().Msg("定期更新任务停止（上下文取消）")
			return
		case <-a.stopChan:
			timer.Stop()
			a.logger.Info().Msg("定期更新任务停止（服务停止）")
			return
		case <-timer.C:
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

		if isSNMPCommand(script) {
			r.agent.logger.Info().Str("callID", callID).Msg("准备处理SNMP特殊请求")
		} else {
			r.agent.logger.Info().
				Str("callID", callID).
				Msgf("准备处理执行请求:\n%s", logScriptSummary(script))
		}

		// 特殊命令直接使用 Go 原生实现，避免调用 PowerShell。
		if isSNMPCommand(script) {
			outputText = executeSNMPCommand(script)
			r.agent.logger.Info().Str("callID", callID).Msg("SNMP操作完成，跳过PowerShell")
		} else if isHardwareInfoCommand(script) {
			var err error
			outputText, err = CollectHardwareInfo()
			if err != nil {
				outputText = "硬件信息采集失败: " + err.Error()
			}
			r.agent.logger.Info().Str("callID", callID).Msg("硬件信息采集完成，跳过PowerShell")
		} else if isWatermarkCommand(script) {
			outputText = HandleWatermarkCommand(script)
			r.agent.logger.Info().Str("callID", callID).Msg("水印命令处理完成，跳过PowerShell")
		} else if isWallpaperCommand(script) {
			outputText = HandleWallpaperCommand(script)
			r.agent.logger.Info().Str("callID", callID).Msg("壁纸命令处理完成，跳过PowerShell")
		} else if isUserSessionCommand(script) {
			// 需要用户可见效果的脚本必须投递到登录用户的交互式桌面：
			// 服务运行在 Session 0，在那里创建窗口或调用 SystemParametersInfo 用户完全看不到。
			outputText = ExecuteScriptInUserSession(script)
			r.agent.logger.Info().Str("callID", callID).Msg("用户会话脚本执行完成")
		} else {
			// 执行PowerShell脚本
			outputText = ExecuteScriptNatively(script)
		}

		if isSNMPCommand(script) {
			r.agent.logger.Info().
				Str("callID", callID).
				Int("resultBytes", len(outputText)).
				Msg("SNMP执行请求已生成结果")
		} else {
			r.agent.logger.Info().
				Str("callID", callID).
				Msgf("执行请求结果:\n%s", outputText)
		}

		// 发送回调
		if err := <-r.agent.client.Send("PowershellScriptCallback", callID, outputText); err != nil {
			r.agent.logger.Error().
				Err(err).
				Str("callID", callID).
				Msg("发送PowershellScriptCallback失败")
		} else {
			r.agent.logger.Info().
				Str("callID", callID).
				Msg("执行请求完成并已回调")
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
