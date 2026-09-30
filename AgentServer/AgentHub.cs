using Microsoft.AspNetCore.SignalR;
using Microsoft.Extensions.Logging;
using System.Collections.Concurrent;
using System.Runtime.InteropServices;
using System.Text;

namespace AgentServer
{
	public class AgentHub(AgentService service, ILogger<AgentHub> logger, IConfiguration configuration) : Hub
	{
		static readonly ConcurrentDictionary<string, TaskCompletionSource<List<string>>> completionCallbacks = new();

		// 兜底校验：正常情况下未通过鉴权的连接已经在HTTP层被401拦掉，
		// 这里再挡一次，防止绕过中间件直连Hub
		public override async Task OnConnectedAsync()
		{
			if (!HubAuth.IsValid(configuration, Context.GetHttpContext()))
			{
				logger.LogWarning("拒绝未授权的AgentHub连接: RemoteIp={RemoteIp}",
					Context.GetHttpContext()?.Connection.RemoteIpAddress);
				Context.Abort();
				return;
			}

			await base.OnConnectedAsync();
		}

		// Agent 与管理端共用同一个 /AgentHub，但两者的凭据不同：
		// Agent 只持有 HubAuthToken，管理端则是通过 /Login 拿到 Cookie 的已认证用户。
		// 不区分调用方的话，任一持有共享令牌的 Agent 都能调用 SendInput 对全舰队下发 PowerShell。
		private bool IsAdminCaller => Context.User?.Identity?.IsAuthenticated == true;

		private void EnsureAdminCaller(string method)
		{
			if (IsAdminCaller)
				return;

			logger.LogWarning("拒绝非管理端调用 {Method}: ConnectionId={ConnectionId}, RemoteIp={RemoteIp}",
				method, Context.ConnectionId, Context.GetHttpContext()?.Connection.RemoteIpAddress);
			throw new HubException($"{method} 仅允许已登录的管理端调用");
		}

		public async Task RegisterTerminal(string agentId)
		{
			EnsureAdminCaller(nameof(RegisterTerminal));

			logger.LogDebug("Term Register: {ConnectionId}", Context.ConnectionId);
			await Clients.Client(agentId).SendAsync("RegisterTerminal", Context.ConnectionId);
		}

		public async Task RegisterAgent(string boardSerial, string version, string ip, string group, int platform, string osArch, string osDesc, string hostName)
		{
			service.Add(new AgentModel
			{
				AgentId = Context.ConnectionId,
				IpAddress = ip,
				BoardSerial = boardSerial,
				Version = version,
				OSPlatform = platform,
				OSArchitecture = osArch,
				OSDescription = osDesc,
				Group = group,
				HostName = hostName
			});
			logger.LogInformation("Agent Register: {ConnectionId}, Serial={Serial}, Host={HostName}", Context.ConnectionId, boardSerial, hostName);
		}

		public async Task UpdateAgent(string boardSerial, string version, string ip, string group, int platform, string osArch, string osDesc, string hostName)
		{
			service.Update(new AgentModel
			{
				AgentId = Context.ConnectionId,
				IpAddress = ip,
				BoardSerial = boardSerial,
				Version = version,
				OSPlatform = platform,
				OSArchitecture = osArch,
				OSDescription = osDesc,
				Group = group,
				HostName = hostName
			});
			// 每台Agent每分钟都会上报一次，属于高频心跳，降到Debug避免刷爆日志
			logger.LogDebug("Agent Update: {ConnectionId}", Context.ConnectionId);
		}

		public async Task PowerShellOutput(string terminalId, string message)
		{
			await Clients.Client(terminalId).SendAsync("ReceiveOutput", message);
		}

		public async Task SendInput(string agentId, string command)
		{
			EnsureAdminCaller(nameof(SendInput));

			logger.LogInformation("执行终端命令: AgentId={AgentId}, TerminalId={TerminalId}", agentId, Context.ConnectionId);
			logger.LogInformation("命令内容:{NewLine}{Command}", Environment.NewLine, command);
			await Clients.Client(agentId).SendAsync("ExecutePowerShell", command, Context.ConnectionId);
		}

		public async Task<List<string>> GetCompletion(string agentId, string commandLine, int cursorPosition)
		{
			EnsureAdminCaller(nameof(GetCompletion));

			var terminalId = Context.ConnectionId;
			var tcs = new TaskCompletionSource<List<string>>();
			completionCallbacks.TryAdd(terminalId, tcs);

			// 触发客户端方法
			await Clients.Client(agentId).SendAsync("RequestCompletion", terminalId, commandLine, cursorPosition);

			// 等待客户端回传结果（最多等10秒）
			var task = await Task.WhenAny(tcs.Task, Task.Delay(10000));
			if (task == tcs.Task)
			{
				var result = await tcs.Task;
				completionCallbacks.TryRemove(terminalId, out _);
				return result;
			}
			else
			{
				completionCallbacks.TryRemove(terminalId, out _);
				throw new TimeoutException("Client did not respond in time.");
			}
		}

		public async Task CompletionCallback(string terminalId, List<string> result)
		{
			if (completionCallbacks.TryRemove(terminalId, out var tcs))
			{
				tcs.TrySetResult(result);
			}
		}

		public void CaptureScreenCallback(string callId, byte[] result)
		{
			// 同时处理PowerShellService的回调
			service.HandleCaptureResult(callId, result);
		}

		public void PrinterDriverInstallCallback(string callId, string result)
		{
			service.HandlePrinterDriverInstallCallback(callId, result);
		}

		public void RemoteDeskCallback(string callId, string result)
		{
			// 同时处理PowerShellService的回调
			service.HandleRemoteDeskCallback(callId, result);
		}

		public void PowershellScriptCallback(string callId, string result)
		{
			// 同时处理PowerShellService的回调
			service.HandleScriptCallback(callId, result);
		}

		public override Task OnDisconnectedAsync(Exception? exception)
		{
			service.Remove(Context.ConnectionId);
			return base.OnDisconnectedAsync(exception);
		}
	}
}