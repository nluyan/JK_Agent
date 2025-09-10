﻿﻿using Microsoft.AspNetCore.SignalR;
using Microsoft.Extensions.Logging;
using System.Collections.Concurrent;
using System.Text;

namespace AgentServer
{
	public class AgentHub(AgentService service) : Hub
	{
		static readonly ConcurrentDictionary<string, TaskCompletionSource<List<string>>> completionCallbacks = new();
		static readonly ConcurrentDictionary<Guid, TaskCompletionSource<string>> powershellScriptCallbacks = new();

		public async Task RegisterTerminal(string agentId)
		{
			Console.WriteLine("Term Register:" + Context.ConnectionId);
			await Clients.Client(agentId).SendAsync("RegisterTerminal", Context.ConnectionId);
		}

		public async Task RegisterAgent(string boardSerial, string version)
		{
			var ip = Context.GetHttpContext()?.Connection.RemoteIpAddress.ToString();
			service.Add(new AgentModel
			{
				AgentId = Context.ConnectionId,
				IpAddress = ip,
				BoardSerial = boardSerial,
				Version = version
			});
			Console.WriteLine("Agent Register:" + Context.ConnectionId);
		}

		public async Task PowerShellOutput(string terminalId, string message)
		{
			await Clients.Client(terminalId).SendAsync("ReceiveOutput", message);
		}

		public async Task SendInput(string agentId, string command)
		{
			await Clients.Client(agentId).SendAsync("ExecutePowerShell", command, Context.ConnectionId);
		}

		public async Task<List<string>> GetCompletion(string agentId, string commandLine, int cursorPosition)
		{
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