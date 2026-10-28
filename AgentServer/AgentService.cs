using Microsoft.AspNetCore.SignalR;
using Microsoft.Extensions.Configuration;
﻿﻿﻿﻿using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace AgentServer
{
	public class AgentService
	{
		private const string HardwareInfoCommand = "__JK_AGENT_COLLECT_HARDWARE_INFO__";
		private const string SnmpCommandPrefix = "__JK_AGENT_SNMP__";
		private const string WatermarkCommandPrefix = "__JK_AGENT_WATERMARK__";
		private const string WallpaperCommandPrefix = "__JK_AGENT_WALLPAPER__";
		private const string UserSessionDirective = "#JK-USERSESSION";
		private const string PowerShellCommandType = "PowerShell";
		private const string SnmpCommandType = "SNMP";
		private const string HardwareInfoCommandType = "HardwareInfo";
		private const string WatermarkCommandType = "Watermark";
		private const string WallpaperCommandType = "Wallpaper";
		private const string UserSessionCommandType = "UserSession";
		// 水印/壁纸命令可以内嵌 base64 图片，写日志前按这个长度截断。
		private const int MaxCommandLogChars = 1024;
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
					_logger.LogInformation("注册Agent到CMDB成功: Serial={Serial}, StatusCode={StatusCode}",
						model.BoardSerial, (int)response.StatusCode);
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
			var requestedScript = string.IsNullOrEmpty(script) ? "<空>" : TruncateBulkCommandForLog(script);

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
						script = EnsureCommandPrefix(script, SnmpCommandPrefix);
					}
					else if (commandType.Equals(WatermarkCommandType, StringComparison.OrdinalIgnoreCase))
					{
						script = EnsureCommandPrefix(script, WatermarkCommandPrefix);
					}
					else if (commandType.Equals(WallpaperCommandType, StringComparison.OrdinalIgnoreCase))
					{
						script = EnsureCommandPrefix(script, WallpaperCommandPrefix);
					}
					else if (commandType.Equals(UserSessionCommandType, StringComparison.OrdinalIgnoreCase))
					{
						// 需要用户看见效果的脚本必须打上指令行，终端才会把它投递到登录用户的
						// 交互式桌面执行；服务本身跑在 Session 0，在那里弹窗用户完全看不到。
						script = EnsureUserSessionDirective(script);
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

				// 水印文案里的机器维度占位符在服务端替换：服务端掌握 AgentId、序列号、分组和上报 IP，
				// 而登录用户只有终端自己知道，所以那部分占位符留给终端在每次投递时替换。
				script = ApplyWatermarkPlaceholders(script, agent);

				var commandName = ResolveCommandName(script);
				Console.WriteLine($"执行Agent命令: 类型={commandName}, AgentId={agent.AgentId}, 请求Id={requestId}");
				Console.WriteLine($"命令内容:\n{TruncateBulkCommandForLog(script)}");
				var tcs = new TaskCompletionSource<string>();
				_scriptCallbacks.TryAdd(requestId, tcs);
				try
				{
					// 发送脚本执行请求到Agent
					await _hubContext.Clients.Client(agent.AgentId).SendAsync("ExecutePowershellScript", requestId, script);

					// 原生硬件采集和 SNMP 操作比普通脚本更耗时，但仍复用同一个执行接口。
					var trimmedScript = script.Trim();
					var isSnmpCommand = trimmedScript.StartsWith(SnmpCommandPrefix, StringComparison.Ordinal);
					// 壁纸可能要先下载图片再切进用户会话设置；用户会话脚本自身也有 30 秒预算。
					// 这两种都必须比终端内部的超时更宽，否则服务端会先于终端判定超时。
					var needsUserSession = trimmedScript.StartsWith(WallpaperCommandPrefix, StringComparison.Ordinal)
						|| trimmedScript.StartsWith(UserSessionDirective, StringComparison.Ordinal);
					// SNMP、水印、壁纸都返回带成功标志的 JSON；其余命令返回自由文本，不能按 JSON 判定。
					var isNativeJsonCommand = isSnmpCommand
						|| trimmedScript.StartsWith(WatermarkCommandPrefix, StringComparison.Ordinal)
						|| trimmedScript.StartsWith(WallpaperCommandPrefix, StringComparison.Ordinal);
					var timeout = string.Equals(trimmedScript, HardwareInfoCommand, StringComparison.Ordinal)
						? TimeSpan.FromSeconds(60)
						: isSnmpCommand
							? TimeSpan.FromMinutes(5)
							: needsUserSession
								? TimeSpan.FromSeconds(60)
								: TimeSpan.FromSeconds(30);
					var task = await Task.WhenAny(tcs.Task, Task.Delay(timeout));
					if (task == tcs.Task)
					{
						var result = await tcs.Task;
						var status = result == "执行结果为空。" || (isNativeJsonCommand && !IsSuccessfulNativeResult(result)) ? 1 : 0;
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

		/// <summary>
		/// 下发屏幕水印命令。服务端不保存任何水印配置，文案随命令下发后由终端自行持久化，
		/// 服务重启或用户强制结束水印进程后，终端看门狗会按本地策略自动恢复。
		/// </summary>
		public Task<ExecuteResult> SetWatermark(WatermarkDTO dto) =>
			ExecutePowershellScript(dto.ToExecuteDTO(), dto.ToCommandJson(), WatermarkCommandType);

		/// <summary>
		/// 下发壁纸命令。图片可 base64 内嵌，也可给 URL 由终端下载；
		/// lock=true 时写入策略键，用户无法在"设置→个性化"里改回自己的壁纸。
		/// </summary>
		public Task<ExecuteResult> SetWallpaper(WallpaperDTO dto) =>
			ExecutePowershellScript(dto.ToExecuteDTO(), dto.ToCommandJson(), WallpaperCommandType);

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

		// 水印与壁纸命令可以内嵌 base64 图片，原样打印会把十几兆字节灌进控制台日志。
		// 其余命令保持完整记录，不改变现有的审计行为。
		private static string TruncateBulkCommandForLog(string script)
		{
			var trimmed = script.TrimStart();
			var isBulkCommand = trimmed.StartsWith(WatermarkCommandPrefix, StringComparison.Ordinal)
				|| trimmed.StartsWith(WallpaperCommandPrefix, StringComparison.Ordinal);
			if (!isBulkCommand || trimmed.Length <= MaxCommandLogChars)
				return script;
			return trimmed.Substring(0, MaxCommandLogChars) + $"...(共{trimmed.Length}字符，已截断)";
		}

		// SNMP 命令用 success 字段、水印与壁纸命令用 ok 字段标记成功，这里统一判定。
		// 终端在参数非法或平台不支持时返回的是自由文本，同样按失败处理。
		private static bool IsSuccessfulNativeResult(string result)
		{
			if (string.IsNullOrWhiteSpace(result))
				return false;

			try
			{
				using var document = JsonDocument.Parse(result);
				if (document.RootElement.ValueKind != JsonValueKind.Object)
					return false;

				foreach (var flag in new[] { "success", "ok" })
				{
					if (document.RootElement.TryGetProperty(flag, out var value))
						return value.ValueKind == JsonValueKind.True;
				}
				return false;
			}
			catch (JsonException)
			{
				return false;
			}
		}

		private static string ResolveCommandName(string script)
		{
			var trimmed = script.Trim();
			if (string.Equals(trimmed, HardwareInfoCommand, StringComparison.Ordinal))
				return HardwareInfoCommandType;

			var prefixes = new (string Prefix, string Name)[]
			{
				(SnmpCommandPrefix, SnmpCommandType),
				(WatermarkCommandPrefix, WatermarkCommandType),
				(WallpaperCommandPrefix, WallpaperCommandType),
				(UserSessionDirective, UserSessionCommandType)
			};
			foreach (var (prefix, name) in prefixes)
			{
				if (trimmed.StartsWith(prefix, StringComparison.Ordinal))
					return name;
			}
			return PowerShellCommandType;
		}

		// 命令前缀只需出现一次：调用方可能已经自己拼好了前缀。
		private static string EnsureCommandPrefix(string script, string prefix)
		{
			if (string.IsNullOrEmpty(script))
				return prefix;
			return script.TrimStart().StartsWith(prefix, StringComparison.Ordinal) ? script : prefix + script;
		}

		// 终端只认脚本首行的指令行，允许前置空行和 BOM，但不认出现在脚本中间的指令。
		private static string EnsureUserSessionDirective(string script)
		{
			if (string.IsNullOrWhiteSpace(script))
				return script;

			var firstLine = script.Split('\n')[0].Trim().TrimStart('\uFEFF');
			if (firstLine.StartsWith(UserSessionDirective, StringComparison.OrdinalIgnoreCase))
				return script;
			return UserSessionDirective + "\n" + script;
		}

		// 把水印文案里服务端才知道的占位符替换成这台机器的真实信息。
		// {UserName}/{SessionId}/{DateTime} 不在这里替换：登录用户会换，必须每次投递时由终端替换。
		private static string ApplyWatermarkPlaceholders(string script, AgentModel agent)
		{
			var head = script.TrimStart();
			if (!head.StartsWith(WatermarkCommandPrefix, StringComparison.Ordinal))
				return script;

			var payload = head.Substring(WatermarkCommandPrefix.Length).Trim();
			if (payload.Length == 0)
				return script;

			try
			{
				var node = JsonNode.Parse(payload)?.AsObject();
				if (node == null)
					return script;

				var text = node["text"]?.GetValue<string>();
				if (string.IsNullOrEmpty(text))
					return script;

				var replaced = SubstituteMachinePlaceholders(text, agent);
				if (string.Equals(replaced, text, StringComparison.Ordinal))
					return script;

				node["text"] = replaced;
				return WatermarkCommandPrefix + node.ToJsonString(AgentCommandJson.Options);
			}
			catch (Exception)
			{
				// 载荷格式不对时原样下发，让终端返回它自己的解析错误，服务端不掩盖问题。
				return script;
			}
		}

		private static string SubstituteMachinePlaceholders(string text, AgentModel agent)
		{
			if (string.IsNullOrEmpty(text) || text.IndexOf('{') < 0)
				return text;

			var replacements = new (string Token, string? Value)[]
			{
				("{HostName}", agent.HostName),
				("{Serial}", agent.BoardSerial),
				("{IP}", agent.IpAddress),
				("{Group}", agent.Group),
				("{AgentId}", agent.AgentId)
			};
			foreach (var (token, value) in replacements)
				text = text.Replace(token, value ?? string.Empty, StringComparison.OrdinalIgnoreCase);
			return text;
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
