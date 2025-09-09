﻿﻿﻿﻿using System.Collections.Concurrent;
using Microsoft.AspNetCore.SignalR;

namespace AgentServer
{
	public class AgentService
	{
		private readonly ConcurrentDictionary<string, AgentModel> agents = new();
		private readonly IHubContext<AgentHub> _hubContext;
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _scriptCallbacks = new();

		public AgentService(IHubContext<AgentHub> hubContext)
		{
			_hubContext = hubContext;
		}

		public void Add(AgentModel model)
		{
			if (!agents.TryAdd(model.AgentId, model))
				throw new Exception("添加Agent失败");
		}

		public void Remove(string agentId)
		{
			if (agents.ContainsKey(agentId))
			{
				if (!agents.TryRemove(agentId, out _))
					throw new Exception("移除Agent失败");
			}
		}

		public List<AgentModel> GetAgents()
		{
			return agents.Values.ToList();
		}
		
		public AgentModel? GetById(string agentId)
		{
			agents.TryGetValue(agentId, out var agent);
			return agent;
		}

		public async Task<string> ExecutePowershellScript(string agentId, string script)
		{
			var requestId = Guid.NewGuid().ToString();
			var tcs = new TaskCompletionSource<string>();
			_scriptCallbacks.TryAdd(requestId, tcs);

			try
			{
				// 发送脚本执行请求到Agent
				await _hubContext.Clients.Client(agentId).SendAsync("ExecutePowershellScript", requestId, script);

				// 等待客户端回传结果（最多等30秒）
				var task = await Task.WhenAny(tcs.Task, Task.Delay(30000));
				if (task == tcs.Task)
				{
					var result = await tcs.Task;
					_scriptCallbacks.TryRemove(requestId, out _);
					return result;
				}
				else
				{
					_scriptCallbacks.TryRemove(requestId, out _);
					throw new TimeoutException("脚本执行超时，Agent未在30秒内返回结果");
				}
			}
			catch (Exception ex)
			{
				_scriptCallbacks.TryRemove(requestId, out _);
				throw new Exception($"执行PowerShell脚本失败: {ex.Message}");
			}
		}

		public void HandleScriptCallback(string requestId, string result)
		{
			if (_scriptCallbacks.TryRemove(requestId, out var tcs))
			{
				tcs.TrySetResult(result);
			}
		}
	}
}
