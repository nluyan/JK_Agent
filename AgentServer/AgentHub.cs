using Microsoft.AspNetCore.SignalR;
using Microsoft.Extensions.Logging;
using System.Collections.Concurrent;

namespace AgentServer
{
	class Agent
	{
		public string IpAddress { get; set; }

		public string AgentId { get; set; }
	}

	public class AgentHub : Hub
	{
		private static readonly ConcurrentDictionary<string, Agent> Agents = new();

		private static readonly ConcurrentDictionary<string, TaskCompletionSource<List<string>>> _callbacks = new();

		public async Task RegisterTerminal(string agentId)
		{
			Console.WriteLine("Term Register:" + Context.ConnectionId);
			await Clients.Client(agentId).SendAsync("RegisterTerminal", Context.ConnectionId);
		}

		public async Task RegisterAgent()
		{
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
			_callbacks.TryAdd(terminalId, tcs);

			// 触发客户端方法
			await Clients.Client(agentId).SendAsync("RequestCompletion", terminalId, commandLine, cursorPosition);

			// 等待客户端回传结果（最多等10秒）
			var task = await Task.WhenAny(tcs.Task, Task.Delay(10000));
			if (task == tcs.Task)
			{
				var result = await tcs.Task;
				return result;
			}
			else
			{
				_callbacks.TryRemove(terminalId, out _);
				throw new TimeoutException("Client did not respond in time.");
			}
		}

		public async Task CompletionCallback(string terminalId, List<string> result)
		{
			if (_callbacks.TryRemove(terminalId, out var tcs))
			{
				tcs.TrySetResult(result);
			}
		}

		public override Task OnDisconnectedAsync(Exception? exception)
		{
			var connectionId = Context.ConnectionId;
			Agents.TryRemove(connectionId, out Agent? value);
			return base.OnDisconnectedAsync(exception);
		}
	}
}