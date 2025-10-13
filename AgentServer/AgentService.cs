﻿﻿﻿﻿using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.RegularExpressions;
using Microsoft.AspNetCore.SignalR;

namespace AgentServer
{
	public class AgentService
	{
		private readonly ConcurrentDictionary<string, AgentModel> agents = new();
		private readonly IHubContext<AgentHub> _hubContext;
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _scriptCallbacks = new();
		private readonly ConcurrentDictionary<string, TaskCompletionSource<byte[]>> _captureCallbacks = new();
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _remoteDeskCallbacks = new();

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

		string GetAgentIdByDTO(DTOBase dto)
		{
			if(string.IsNullOrWhiteSpace(dto.AgentId) && string.IsNullOrWhiteSpace(dto.Serial) && string.IsNullOrWhiteSpace(dto.Ip))
				throw new Exception("AgentId, Serial, IP不能同时为空");
			var agentId = dto.AgentId;
			if (agentId == null)
			{
				if (!string.IsNullOrWhiteSpace(dto.Serial))
				{
					var agent = agents.Values.Where(c => c.BoardSerial == dto.Serial).FirstOrDefault();
					if (agent == null)
						throw new Exception("未找到对应的Agent\n" + JsonSerializer.Serialize(dto));
					agentId = agent.AgentId;
				}
				else if(!string.IsNullOrWhiteSpace(dto.Ip))
				{
					var agent = agents.Values.Where(c => c.IpAddress.Contains(dto.Ip)).FirstOrDefault();
					if (agent == null)
						throw new Exception("未找到对应的Agent\n" + JsonSerializer.Serialize(dto));
					agentId = agent.AgentId;
				}
			}
			return agentId;
		}

		public async Task<string> ExecutePowershellScript(ExecuteDTO dto, string script)
		{
			var agentId = GetAgentIdByDTO(dto);
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

		public async Task<byte[]> CaptureScreen(ScreenDTO dto)
		{
			var agentId = GetAgentIdByDTO(dto);
			var requestId = Guid.NewGuid().ToString();
			var tcs = new TaskCompletionSource<byte[]>();
			_captureCallbacks.TryAdd(requestId, tcs);

			try
			{
				await _hubContext.Clients.Client(agentId).SendAsync("CaptureScreen", requestId);
				var task = await Task.WhenAny(tcs.Task, Task.Delay(50000));
				if (task == tcs.Task)
				{
					var result = await tcs.Task;
					return result;
				}
				else
				{
					throw new TimeoutException("截屏执行超时，Agent未在30秒内返回结果");
				}
			}
			catch (Exception ex)
			{
				throw new Exception($"截屏失败: {ex.Message}");
			}
			finally
			{
				_captureCallbacks.TryRemove(requestId, out _);
			}
		}

		public async Task<string> RemoteDesk(RemoteDeskDTO dto, string server, string key)
		{
			var agentId = GetAgentIdByDTO(dto);
			var requestId = Guid.NewGuid().ToString();
			var tcs = new TaskCompletionSource<string>();
			_remoteDeskCallbacks.TryAdd(requestId, tcs);

			try
			{
				// 发送脚本执行请求到Agent
				await _hubContext.Clients.Client(agentId).SendAsync("RemoteDesk", requestId, server, key);

				// 等待客户端回传结果（最多等30秒）
				var task = await Task.WhenAny(tcs.Task, Task.Delay(30000));
				if (task == tcs.Task)
				{
					var result = await tcs.Task;
					_remoteDeskCallbacks.TryRemove(requestId, out _);
					return result;
				}
				else
				{
					_remoteDeskCallbacks.TryRemove(requestId, out _);
					throw new TimeoutException("执行超时，Agent未在30秒内返回结果");
				}
			}
			catch (Exception ex)
			{
				_remoteDeskCallbacks.TryRemove(requestId, out _);
				throw new Exception($"启动远程桌面失败: {ex.Message}");
			}
		}

		public void HandleCaptureResult(string requestId, byte[] imageData)
		{
			if (_captureCallbacks.TryRemove(requestId, out var tcs))
			{
				tcs.TrySetResult(imageData);
			}
		}

		public void HandleScriptCallback(string requestId, string result)
		{
			if (_scriptCallbacks.TryRemove(requestId, out var tcs))
			{
				tcs.TrySetResult(result);
			}
		}

		public void HandleRemoteDeskCallback(string requestId, string result)
		{
			if (_remoteDeskCallbacks.TryRemove(requestId, out var tcs))
			{
				tcs.TrySetResult(result);
			}
		}
	}
}
