using Microsoft.AspNetCore.SignalR;
using Microsoft.Extensions.Configuration;
﻿﻿﻿﻿using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace AgentServer
{
	public class AgentService
	{
		private const string HardwareInfoCommand = "__JK_AGENT_COLLECT_HARDWARE_INFO__";
		private const string SnmpCommandPrefix = "__JK_AGENT_SNMP__";
		private const string PowerShellCommandType = "PowerShell";
		private const string SnmpCommandType = "SNMP";
		private const string HardwareInfoCommandType = "HardwareInfo";
		private readonly ConcurrentDictionary<string, AgentModel> agents = new();
		// BoardSerial -> 当前在线的AgentId：用于识别同一台机器的重连，避免列表里出现重复和幽灵条目
		private readonly ConcurrentDictionary<string, string> agentIdBySerial = new();
		private readonly IHubContext<AgentHub> _hubContext;
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _scriptCallbacks = new();
		private readonly ConcurrentDictionary<string, TaskCompletionSource<byte[]>> _captureCallbacks = new();
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _remoteDeskCallbacks = new();
		private readonly ConcurrentDictionary<string, TaskCompletionSource<string>> _printerDriverInstallCallbacks = new();

		// CMDB上报专用HttpClient名称，配合IHttpClientFactory复用连接
		public const string CmdbHttpClientName = "cmdb";

		IConfiguration config;
		private readonly IHttpClientFactory _httpClientFactory;
		private readonly ILogger<AgentService> _logger;

		public AgentService(IHubContext<AgentHub> hubContext, IConfiguration config, IHttpClientFactory httpClientFactory, ILogger<AgentService> logger)
		{
			this.config = config;
			_hubContext = hubContext;
			_httpClientFactory = httpClientFactory;
			_logger = logger;
		}

		public void Add(AgentModel model)
		{
			// 只有真正新接入的机器才上报CMDB，重连不再重复上报
			if (UpsertAgent(model))
				NotifyCmdbAccess(model);
		}

		public void Update(AgentModel model)
		{
			// 周期性上报同样走Upsert：服务端重启后内存表被清空时也能把Agent补回来，
			// 且同一台机器重连后不会残留旧连接的幽灵条目
			UpsertAgent(model);
		}

		/// <summary>
		/// 写入Agent信息，同一台机器（BoardSerial相同）只保留最新的那条连接。
		/// </summary>
		/// <returns>true表示这是一台新接入的机器，false表示重连或已有连接的信息更新</returns>
		private bool UpsertAgent(AgentModel model)
		{
			var serial = model.BoardSerial?.Trim() ?? string.Empty;

			// 没有序列号时无法判断是否重连，退化为按连接维度保存
			if (serial.Length == 0)
			{
				var knownWithoutSerial = agents.ContainsKey(model.AgentId);
				agents[model.AgentId] = model;
				return !knownWithoutSerial;
			}

			if (agentIdBySerial.TryGetValue(serial, out var currentAgentId))
			{
				if (currentAgentId != model.AgentId)
				{
					// 同一台机器重连、旧连接还没超时断开：丢弃旧条目，
					// 避免列表出现重复Agent，也避免按序列号/IP查找命令目标时命中已断开的旧连接
					agents.TryRemove(currentAgentId, out _);
					_logger.LogInformation("Agent重连，替换旧连接条目: Serial={Serial}, {OldAgentId} -> {NewAgentId}",
						serial, currentAgentId, model.AgentId);
				}

				agentIdBySerial[serial] = model.AgentId;
				agents[model.AgentId] = model;
				return false;
			}

			agentIdBySerial[serial] = model.AgentId;
			agents[model.AgentId] = model;
			return true;
		}

		public void Remove(string agentId)
		{
			if (!agents.TryRemove(agentId, out var removed))
				return;

			var serial = removed.BoardSerial?.Trim() ?? string.Empty;
			if (serial.Length > 0)
			{
				// 只有序列号仍指向这条连接时才清理索引：重连场景下旧连接的断开事件
				// 可能晚于新连接的注册到达，此时不能把新条目一起删掉
				agentIdBySerial.TryRemove(new KeyValuePair<string, string>(serial, agentId));
			}
		}

		public List<AgentModel> GetAgents()
		{
			_logger.LogDebug("获取Agent列表，共计 {Count} 个Agent", agents.Count);
			return agents.Values.ToList();
		}

		// 上报新接入的Agent到CMDB；通过IHttpClientFactory复用连接，避免每次注册都新建HttpClient耗尽端口
		private void NotifyCmdbAccess(AgentModel model)
		{
			var apiUrl = config["cmdbApi"];
			if (string.IsNullOrWhiteSpace(apiUrl))
				return;

			var serverId = config["serverId"];
			_ = Task.Run(async () =>
			{
				try
				{
					var httpClient = _httpClientFactory.CreateClient(CmdbHttpClientName);
					var content = new StringContent(JsonSerializer.Serialize(new
					{
						type = "Access",
						serverId,
						deviceInfo = model
					}), System.Text.Encoding.UTF8, "application/json");
					var response = await httpClient.PostAsync(apiUrl, content);
					response.EnsureSuccessStatusCode();
				}
				catch (Exception ex)
				{
					_logger.LogWarning(ex, "注册Agent到CMDB失败: Serial={Serial}", model.BoardSerial);
				}
			});
		}

		public AgentModel? GetById(string agentId)
		{
			agents.TryGetValue(agentId, out var agent);
			return agent;
		}

		private bool TryGetAgentByDTO(DTOBase? dto, out AgentModel? agent, out string error)
		{
			agent = null;
			error = string.Empty;

			if (dto == null)
			{
				error = "请求内容不能为空";
				return false;
			}

			if (string.IsNullOrWhiteSpace(dto.AgentId)
				&& string.IsNullOrWhiteSpace(dto.Serial)
				&& string.IsNullOrWhiteSpace(dto.Ip))
			{
				error = "AgentId, Serial, IP不能同时为空";
				return false;
			}

			if (!string.IsNullOrWhiteSpace(dto.Serial))
			{
				agent = agents.Values.FirstOrDefault(c => c.BoardSerial == dto.Serial);
			}
			else if (!string.IsNullOrWhiteSpace(dto.Ip))
			{
				agent = agents.Values.FirstOrDefault(c => c.IpAddress.Contains(dto.Ip));
			}
			else
			{
				agent = agents.GetValueOrDefault(dto.AgentId);
			}

			if (agent == null)
			{
				error = "未找到对应的Agent";
				return false;
			}

			return true;
		}

		public async Task<ExecuteResult> ExecutePowershellScript(ExecuteDTO dto, string script, string? commandType = null)
		{
			var requestId = Guid.NewGuid().ToString();
			var requestedCommandType = string.IsNullOrWhiteSpace(commandType) ? PowerShellCommandType : commandType.Trim();
			var requestedTarget = dto == null ? "<null>" : JsonSerializer.Serialize(new
			{
				dto.AgentId,
				Serial = dto.Serial,
				Ip = dto.Ip
			});
			var requestedScript = string.IsNullOrEmpty(script) ? "<空>" : script;

			// 在参数校验和查找 Agent 之前记录原始请求，确保失败请求也能看到用户提交的命令。
			Console.WriteLine($"收到Agent命令: 类型={requestedCommandType}, 请求Id={requestId}, 目标={requestedTarget}");
			Console.WriteLine($"用户命令内容:\n{requestedScript}");

			try
			{
				commandType = commandType?.Trim();
				if (!string.IsNullOrWhiteSpace(commandType))
				{
					if (commandType.Equals(SnmpCommandType, StringComparison.OrdinalIgnoreCase))
					{
						var commandText = script.TrimStart();
						if (!commandText.StartsWith(SnmpCommandPrefix, StringComparison.Ordinal))
							script = SnmpCommandPrefix + script;
					}
					else if (commandType.Equals(HardwareInfoCommandType, StringComparison.OrdinalIgnoreCase))
					{
						script = HardwareInfoCommand;
					}
					else if (!commandType.Equals(PowerShellCommandType, StringComparison.OrdinalIgnoreCase))
					{
						Console.WriteLine($"Agent命令参数无效: 类型={requestedCommandType}, 请求Id={requestId}, 错误=不支持的Agent命令类型");
						return new ExecuteResult { Status = 1, Result = $"不支持的Agent命令类型: {commandType}" };
					}
				}

				if (string.IsNullOrWhiteSpace(script))
				{
					Console.WriteLine($"Agent命令参数无效: 类型={requestedCommandType}, 请求Id={requestId}, 错误=脚本内容为空");
					return new ExecuteResult { Status = 1, Result = "脚本内容不能为空" };
				}

				if (!TryGetAgentByDTO(dto, out var agent, out var agentError) || agent == null)
				{
					Console.WriteLine($"Agent命令目标无效: 类型={requestedCommandType}, 请求Id={requestId}, 目标={requestedTarget}, 错误={agentError}");
					return new ExecuteResult { Status = 1, Result = agentError };
				}

				var commandName = string.Equals(script.Trim(), HardwareInfoCommand, StringComparison.Ordinal)
					? HardwareInfoCommandType
					: script.TrimStart().StartsWith(SnmpCommandPrefix, StringComparison.Ordinal)
						? SnmpCommandType
						: PowerShellCommandType;
				Console.WriteLine($"执行Agent命令: 类型={commandName}, AgentId={agent.AgentId}, 请求Id={requestId}");
				Console.WriteLine($"命令内容:\n{script}");
				var tcs = new TaskCompletionSource<string>();
				_scriptCallbacks.TryAdd(requestId, tcs);
				try
				{
					// 发送脚本执行请求到Agent
					await _hubContext.Clients.Client(agent.AgentId).SendAsync("ExecutePowershellScript", requestId, script);

					// 原生硬件采集和 SNMP 操作比普通脚本更耗时，但仍复用同一个执行接口。
					var trimmedScript = script.Trim();
					var isSnmpCommand = trimmedScript.StartsWith(SnmpCommandPrefix, StringComparison.Ordinal);
					var timeout = string.Equals(trimmedScript, HardwareInfoCommand, StringComparison.Ordinal)
						? TimeSpan.FromSeconds(60)
						: isSnmpCommand
							? TimeSpan.FromMinutes(5)
							: TimeSpan.FromSeconds(30);
					var task = await Task.WhenAny(tcs.Task, Task.Delay(timeout));
					if (task == tcs.Task)
					{
						var result = await tcs.Task;
						var status = result == "执行结果为空。" || (isSnmpCommand && !IsSuccessfulSnmpResult(result)) ? 1 : 0;
						Console.WriteLine($"Agent命令执行完成: 类型={commandName}, AgentId={agent.AgentId}, 请求Id={requestId}, 状态={status}");
						return new ExecuteResult { Status = status, Result = result };
					}
					else
					{
						Console.WriteLine($"Agent命令执行超时: 类型={commandName}, AgentId={agent.AgentId}, 请求Id={requestId}, 超时={timeout.TotalSeconds:0}秒");
						return new ExecuteResult { Status = 1, Result = $"脚本执行超时，Agent未在{timeout.TotalSeconds:0}秒内返回结果" };
					}
				}
				catch (Exception ex)
				{
					Console.WriteLine($"Agent命令执行失败: 类型={commandName}, AgentId={agent.AgentId}, 请求Id={requestId}, 错误={ex.Message}");
					return new ExecuteResult { Status = 1, Result = $"执行PowerShell脚本失败: {ex.Message}" };
				}
				finally
				{
					_scriptCallbacks.TryRemove(requestId, out _);
				}
			}
			catch (Exception ex)
			{
				Console.WriteLine($"Agent命令处理失败: 类型={requestedCommandType}, 请求Id={requestId}, 错误={ex.Message}");
				return new ExecuteResult { Status = 1, Result = $"执行Agent命令失败: {ex.Message}" };
			}
		}

		public async Task<ExecuteResult> InstallPrinterDriver(PrinterDriverInstallDTO dto)
		{
			if (string.IsNullOrWhiteSpace(dto.DriverUrl))
				return new ExecuteResult { Status = 1, Result = "驱动包地址不能为空" };

			if (!Uri.TryCreate(dto.DriverUrl, UriKind.Absolute, out var driverUri)
				|| (driverUri.Scheme != Uri.UriSchemeHttp && driverUri.Scheme != Uri.UriSchemeHttps))
				return new ExecuteResult { Status = 1, Result = "驱动包地址必须是有效的 HTTP 或 HTTPS 地址" };

			if (!TryGetAgentByDTO(dto, out var agent, out var agentError) || agent == null)
			{
				Console.WriteLine($"Agent命令目标无效: 类型=PrinterDriverInstall, 错误={agentError}");
				return new ExecuteResult { Status = 1, Result = agentError };
			}

			var requestId = Guid.NewGuid().ToString();
			Console.WriteLine($"执行Agent命令: 类型=PrinterDriverInstall, AgentId={agent.AgentId}, 请求Id={requestId}, DriverUrl={dto.DriverUrl}");
			var tcs = new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
			_printerDriverInstallCallbacks.TryAdd(requestId, tcs);

			try
			{
				await _hubContext.Clients.Client(agent.AgentId)
					.SendAsync("InstallPrinterDriver", requestId, dto.DriverUrl);

				var timeout = TimeSpan.FromMinutes(10);
				var task = await Task.WhenAny(tcs.Task, Task.Delay(timeout));
				if (task == tcs.Task)
				{
					var result = await tcs.Task;
					var success = IsSuccessfulPrinterDriverResult(result);
					Console.WriteLine($"Agent命令执行完成: 类型=PrinterDriverInstall, AgentId={agent.AgentId}, 请求Id={requestId}, 状态={(success ? 0 : 1)}");
					return new ExecuteResult { Status = success ? 0 : 1, Result = result };
				}

				Console.WriteLine($"Agent命令执行超时: 类型=PrinterDriverInstall, AgentId={agent.AgentId}, 请求Id={requestId}, 超时={timeout.TotalMinutes:0}分钟");
				return new ExecuteResult
				{
					Status = 1,
					Result = $"打印机驱动安装超时，Agent未在{timeout.TotalMinutes:0}分钟内返回结果"
				};
			}
			catch (Exception ex)
			{
				Console.WriteLine($"Agent命令执行失败: 类型=PrinterDriverInstall, AgentId={agent.AgentId}, 请求Id={requestId}, 错误={ex.Message}");
				return new ExecuteResult { Status = 1, Result = $"安装打印机驱动失败: {ex.Message}" };
			}
			finally
			{
				_printerDriverInstallCallbacks.TryRemove(requestId, out _);
			}
		}

		private static bool IsSuccessfulPrinterDriverResult(string result)
		{
			if (string.IsNullOrWhiteSpace(result))
				return false;

			try
			{
				using var document = JsonDocument.Parse(result);
				return document.RootElement.TryGetProperty("Success", out var success)
					&& success.ValueKind == JsonValueKind.True;
			}
			catch (JsonException)
			{
				return false;
			}
		}

		private static bool IsSuccessfulSnmpResult(string result)
		{
			if (string.IsNullOrWhiteSpace(result))
				return false;

			try
			{
				using var document = JsonDocument.Parse(result);
				return document.RootElement.TryGetProperty("success", out var success)
					&& success.ValueKind == JsonValueKind.True;
			}
			catch (JsonException)
			{
				return false;
			}
		}

		public async Task<string> RemoteDesk(RemoteDeskDTO dto)
		{
			if (!TryGetAgentByDTO(dto, out var agent, out var agentError))
				throw new Exception(agentError);

			var requestId = Guid.NewGuid().ToString();
			var tcs = new TaskCompletionSource<string>();
			_remoteDeskCallbacks.TryAdd(requestId, tcs);

			var key = config["RemoteDeskKey"];
			var servers = config.GetSection("RemoteDeskServers").Get<List<RemoteDeskServer>>();
			var server = servers.FirstOrDefault(c => c.Group.Equals(agent.Group, StringComparison.OrdinalIgnoreCase));
			if(server == null)
				server = servers.FirstOrDefault(c => c.Group.Equals("Default", StringComparison.OrdinalIgnoreCase));

			Console.WriteLine($"执行Agent命令: 类型=RemoteDesk, AgentId={agent.AgentId}, 请求Id={requestId}, Server={server?.Server}");

			try
			{
				// 发送脚本执行请求到Agent
				await _hubContext.Clients.Client(agent.AgentId).SendAsync("RemoteDesk", requestId, server.Server, key);

				// 等待客户端回传结果（最多等30秒）
				var task = await Task.WhenAny(tcs.Task, Task.Delay(30000));
				if (task == tcs.Task)
				{
					var result = await tcs.Task;
					Console.WriteLine($"Agent命令执行完成: 类型=RemoteDesk, AgentId={agent.AgentId}, 请求Id={requestId}");
					return result;
				}
				else
				{
					Console.WriteLine($"Agent命令执行超时: 类型=RemoteDesk, AgentId={agent.AgentId}, 请求Id={requestId}, 超时=30秒");
					throw new TimeoutException("执行超时，Agent未在30秒内返回结果");
				}
			}
			catch (Exception ex)
			{
				Console.WriteLine($"Agent命令执行失败: 类型=RemoteDesk, AgentId={agent.AgentId}, 请求Id={requestId}, 错误={ex.Message}");
				throw new Exception($"启动远程桌面失败: {ex.Message}");
			}
			finally
			{
				_remoteDeskCallbacks.TryRemove(requestId, out _);
			}
		}
		public class RemoteDeskServer
		{
			public string Group { get; set; } = string.Empty;
			public string Server { get; set; } = string.Empty;
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

		public void HandlePrinterDriverInstallCallback(string requestId, string result)
		{
			if (_printerDriverInstallCallbacks.TryRemove(requestId, out var tcs))
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
