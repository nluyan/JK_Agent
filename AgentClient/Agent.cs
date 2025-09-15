using AgentClient;
using Microsoft.AspNetCore.SignalR.Client;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.PowerShell;
using Serilog;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Management;
using System.Management.Automation;
using System.Management.Automation.Runspaces;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.RegularExpressions;

internal class Agent
{
	ConcurrentDictionary<string, PowerShell> shells = new();

	string serverUrl;
	string group;

	public Agent(string url, string group)
	{
		serverUrl = url;
		this.group = group;
	}
	public async Task Start()
	{
		HubConnection connection = null;

		// 无限循环确保Agent永不退出
		while (true)
		{
			try
			{
				Log.Debug("正在初始化SignalR连接...");

				connection = new HubConnectionBuilder()
					.WithUrl(serverUrl)
					.AddMessagePackProtocol()
					.WithAutomaticReconnect(new RetryPolicy())
					.Build();

				// 添加连接状态监控事件
				connection.Closed += async (error) =>
				{
					Log.Warning($"连接已断开: {error?.Message ?? "未知原因"}");
					if (error != null)
					{
						Log.Debug($"错误类型: {error.GetType().Name}");
						Log.Debug($"堆栈跟踪: {error.StackTrace}");
					}
					
					// 立即尝试一次重连（不等待监控任务）
					Log.Information("立即尝试重连...");
					try
					{
						await Task.Delay(1000); // 等待1秒
						if (connection.State == HubConnectionState.Disconnected)
						{
							await connection.StartAsync();
							await connection.InvokeAsync("RegisterAgent", GetBoardSerial(), 
								Settings.Version, GetFirstIpv4(), group);
							Log.Information("立即重连成功");
						}
					}
					catch (Exception ex)
					{
						Log.Warning($"立即重连失败: {ex.Message}，将由监控任务继续尝试");
					}
				};

				connection.Reconnecting += (error) =>
				{
					Log.Debug($"发送错误：{error?.Message ?? "连接丢失"}");
					return Task.CompletedTask;
				};

				connection.Reconnected += async (connectionId) =>
				{
					Log.Debug($"重连成功，新连接ID: {connectionId}");
					// 重连成功后重新注册代理
					try
					{
						await connection.InvokeAsync("RegisterAgent", GetBoardSerial(), Settings.Version, GetFirstIpv4(), group);
						Log.Debug("代理重新注册成功");
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"代理重新注册失败: {ex.Message}");
					}
				};

				connection.On<string>("CaptureScreen", async requestId =>
				{
					if (connection.State == HubConnectionState.Connected)
					{
						var data = ScreenCapture.Capture();
						await connection.SendAsync("CaptureScreenCallback", requestId, data);
					}
				});

				connection.On<string>("RegisterTerminal", async terminalId =>
				{
					try
					{
						var ps = PowerShell.Create();
						shells.TryAdd(terminalId, ps);

						if (connection.State == HubConnectionState.Connected)
						{
							await connection.SendAsync("PowerShellOutput", terminalId, GetPowerShellPath(ps));
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"注册终端失败 {terminalId}: {ex.Message}");
					}
				});

				connection.On<string>("TerminalClosed", async terminalId =>
				{
					try
					{
						if (shells.TryRemove(terminalId, out var ps))
						{
							ps.Dispose();
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"关闭终端失败 {terminalId}: {ex.Message}");
					}
				});

				connection.On<string, string>("ExecutePowerShell", async (command, terminalId) =>
				{
					try
					{
						if (!shells.TryGetValue(terminalId, out var ps))
						{
							Log.Error($"终端不存在: {terminalId}");
							return;
						}

						if (ps.InvocationStateInfo.State == PSInvocationState.Running)
						{
							return;
						}
						ps.Commands.Clear();
						ps.Streams.ClearStreams();
						ps.AddScript(command);
						ps.AddCommand("Out-String").AddParameter("Stream");
						var output = new StringBuilder();

						try
						{
							var results = await ps.InvokeAsync();
							if (ps.Streams.Error.Count > 0)
							{
								foreach (var error in ps.Streams.Error)
								{
									output.AppendLine(error.ToString());
								}
							}
							else
							{
								foreach (var item in results)
								{
									output.AppendLine(item.ToString());
								}
							}
						}
						catch (Exception ex)
						{
							Log.Error(ex, "Critical execution error: " + ex.Message);
							output.AppendLine("Critical execution error: " + ex.Message);
						}
						finally
						{
							try
							{
								if (output.Length > 0 && connection?.State == HubConnectionState.Connected)
								{
									await connection.SendAsync("PowerShellOutput", terminalId, output.ToString());
								}
								if (connection?.State == HubConnectionState.Connected)
								{
									await connection.SendAsync("PowerShellOutput", terminalId, GetPowerShellPath(ps));
								}
							}
							catch (Exception ex)
							{
								Log.Error(ex, $"发送PowerShell输出失败: {ex.Message}");
							}
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"执行PowerShell命令失败: {ex.Message}");
					}
				});

				connection.On<string, string, int>("RequestCompletion", async (terminalId, commandLine, cursorPosition) =>
				{
					try
					{
						if (!shells.TryGetValue(terminalId, out var ps))
						{
							if (connection?.State == HubConnectionState.Connected)
							{
								await connection.SendAsync("CompletionCallback", terminalId, new List<string>());
							}
							return;
						}

						var completionResult = CommandCompletion.CompleteInput(commandLine, cursorPosition, null, ps);
						var list = completionResult.CompletionMatches.Select(m => m.CompletionText).ToList();

						if (connection?.State == HubConnectionState.Connected)
						{
							await connection.SendAsync("CompletionCallback", terminalId, list);
						}
					}
					catch (Exception ex)
					{
						Log.Debug($"请求命令补全失败: {ex.Message}");
						try
						{
							if (connection?.State == HubConnectionState.Connected)
							{
								await connection.SendAsync("CompletionCallback", terminalId, new List<string>());
							}
						}
						catch (Exception sendEx)
						{
							Log.Error(sendEx, $"发送空补全结果失败: {sendEx.Message}");
						}
					}
				});

				connection.On<string, string>("ExecutePowershellScript", async (callId, script) =>
				{
					try
					{
						Log.Debug($"开始执行PowerShell脚本，CallId: {callId}");

						var iss = InitialSessionState.CreateDefault();
						iss.ExecutionPolicy = ExecutionPolicy.Unrestricted;

						// 把当前机器所有模块目录下的 *.psd1 一次性塞进列表
						//var allDirs = Environment.GetEnvironmentVariable("PSModulePath").Split(';');
						//var allModules = allDirs
						//				 .Where(Directory.Exists)
						//				 .SelectMany(Directory.EnumerateDirectories)
						//				 .SelectMany(d => Directory.EnumerateFiles(d, "*.psd1"))
						//				 .Select(Path.GetFileNameWithoutExtension)
						//				 .Distinct(StringComparer.OrdinalIgnoreCase)
						//				 .ToArray();

						//iss.ImportPSModule(allModules);

						// 2. 打开 Runspace
						using var runspace = RunspaceFactory.CreateRunspace(iss);
						runspace.Open();

						using (var ps = PowerShell.Create())
						{
							ps.Runspace = runspace;
							ps.AddScript(script);
							ps.AddCommand("Out-String").AddParameter("Stream");
							var output = new StringBuilder();

							try
							{
								var results = await ps.InvokeAsync();
								if (ps.Streams.Error.Count > 0)
								{
									foreach (var error in ps.Streams.Error)
									{
										output.AppendLine(error.ToString());
									}
								}
								else
								{
									foreach (var item in results)
									{
										if (item != null)
										{
											var itemText = item.ToString();
											if (!string.IsNullOrEmpty(itemText))
											{
												output.AppendLine(itemText);
											}
										}
									}
								}
							}
							catch (Exception ex)
							{
								Log.Error(ex, "Critical execution error: " + ex.Message);
								output.AppendLine("Critical execution error: " + ex.Message);
							}

							var outputText = output.ToString();
							Log.Debug($"PowerShell执行完成，输出长度: {outputText.Length} 字节");

							// 检查连接状态
							if (connection?.State != HubConnectionState.Connected)
							{
								Log.Warning($"连接状态异常: {connection?.State}，无法发送结果");
								return;
							}

							try
							{
								await connection.SendAsync("PowershellScriptCallback", callId, outputText);
							}
							catch (Exception sendEx)
							{
								Log.Error(sendEx, $"发送PowerShell脚本结果失败: {sendEx.Message}");
							}
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"执行PowerShell脚本失败 {callId}: {ex.Message}");
					}
				});

				string GetPowerShellPath(PowerShell ps)
				{
					try
					{
						if (ps == null) return "PS>";

						ps.Commands.Clear();
						ps.Streams.ClearStreams();
						var result = ps.AddScript("prompt").Invoke<string>();
						return result?.FirstOrDefault() ?? "PS>";
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"获取PowerShell路径失败: {ex.Message}");
						return "PS>";
					}
				}

				string GetBoardSerial()
				{
					try
					{
						using var searcher = new ManagementObjectSearcher("SELECT SerialNumber FROM Win32_BaseBoard");
						foreach (ManagementObject mo in searcher.Get())
						{
							var sn = mo["SerialNumber"]?.ToString()?.Trim();
							if (!string.IsNullOrWhiteSpace(sn) && !sn.Equals("To be filled by O.E.M.", StringComparison.OrdinalIgnoreCase))
								return sn;
						}
					}
					catch { /* 忽略异常，降级返回空串 */ }
					return string.Empty;
				}

				try
				{
					await connection.StartAsync();
					Log.Debug("已连接到服务器...");
					await connection.InvokeAsync("RegisterAgent", GetBoardSerial(), Settings.Version, GetFirstIpv4(), group);
					Log.Debug("代理注册成功");
				}
				catch (Exception ex)
				{
					Log.Error(ex, $"连接失败：{ex.Message}");
					Log.Debug("程序将继续运行，等待自动重连...");
				}

				// 添加连接状态监控循环
				var cancellationTokenSource = new CancellationTokenSource();
				var monitorTask = Task.Run(async () =>
				{
					try
					{
						var consecutiveFailures = 0;
						var maxConsecutiveFailures = 10; // 最大连续失败次数
						
						while (!cancellationTokenSource.Token.IsCancellationRequested)
						{
							try
							{
								// 根据连续失败次数调整检查间隔
								var checkInterval = consecutiveFailures > 5 ? 30000 : 5000; // 5秒或30秒
								await Task.Delay(checkInterval, cancellationTokenSource.Token);
								
								if (connection?.State == HubConnectionState.Disconnected)
								{
									Log.Warning($"检测到连接断开，尝试第{consecutiveFailures + 1}次重新连接...");
									try
									{
										// 尝试重新建立连接
										await connection.StartAsync();
										await connection.InvokeAsync("RegisterAgent", 
											GetBoardSerial(), Settings.Version, GetFirstIpv4(), group);
										Log.Information("手动重连成功");
										consecutiveFailures = 0; // 重置失败计数
									}
									catch (Exception ex)
									{
										consecutiveFailures++;
										Log.Warning($"第{consecutiveFailures}次重连失败: {ex.Message}");
										
										// 如果连续失败次数过多，可能需要重新创建连接对象
										if (consecutiveFailures >= maxConsecutiveFailures)
										{
											Log.Warning("连续重连失败次数过多，将触发Agent重启...");
											cancellationTokenSource.Cancel(); // 触发Agent重启
											return;
										}
									}
								}
								else if (connection?.State == HubConnectionState.Connected)
								{
									// 连接正常，重置失败计数
									if (consecutiveFailures > 0)
									{
										Log.Information("连接已恢复正常");
										consecutiveFailures = 0;
									}
								}
								else if (connection?.State == HubConnectionState.Connecting || 
								         connection?.State == HubConnectionState.Reconnecting)
								{
									// 正在连接中，不做额外操作，但记录状态
									Log.Debug($"连接状态: {connection?.State}");
								}
							}
							catch (TaskCanceledException)
							{
								break;
							}
							catch (Exception ex)
							{
								consecutiveFailures++;
								Log.Error(ex, $"连接监控过程中发生错误 (第{consecutiveFailures}次): {ex.Message}");
								
								// 如果监控过程本身出现太多异常，也触发重启
								if (consecutiveFailures >= maxConsecutiveFailures)
								{
									Log.Error("连接监控异常次数过多，将触发Agent重启...");
									cancellationTokenSource.Cancel();
									return;
								}
							}
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"连接监控任务异常退出: {ex.Message}");
					}
				}, cancellationTokenSource.Token);

				Log.Information("代理服务已启动，持续运行中...");

				try
				{
					// 持续运行，直到发生异常或被取消
					await Task.Delay(Timeout.Infinite, cancellationTokenSource.Token);
				}
				catch (TaskCanceledException)
				{
					Log.Warning("Agent服务被取消，准备重启...");
					throw; // 抛出异常，触发外层重启逻辑
				}
				catch (Exception ex)
				{
					Log.Error(ex, $"Agent服务运行过程中发生错误: {ex.Message}");
					throw; // 抛出异常，触发重启
				}
				finally
				{
					try
					{
						cancellationTokenSource.Cancel();
						if (connection != null)
						{
							await connection.StopAsync();
							await connection.DisposeAsync();
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"停止连接时发生错误: {ex.Message}");
					}
				}

			}
			catch (Exception ex)
			{
				Log.Error(ex, $"Agent服务异常: {ex.Message}");
				Log.Warning("5秒后将重新启动Agent服务...");

				try
				{
					if (connection != null)
					{
						await connection.DisposeAsync();
					}
				}
				catch (Exception disposeEx)
				{
					Log.Error(disposeEx, $"清理连接资源失败: {disposeEx.Message}");
				}

				try
				{
					await Task.Delay(5000); // 等待5秒后重试
				}
				catch (Exception delayEx)
				{
					Log.Error(delayEx, $"延时等待异常: {delayEx.Message}");
				}

				// 继续循环，重新启动Agent
			}
		}
	}

	//PowerShell CreatePowerShell()
	//{
	//	var iss = InitialSessionState.CreateDefault();
	//	iss.ExecutionPolicy = ExecutionPolicy.Unrestricted;

	//	// 把当前机器所有模块目录下的 *.psd1 一次性塞进列表
	//	//var allDirs = Environment.GetEnvironmentVariable("PSModulePath").Split(';');
	//	//var allModules = allDirs
	//	//				 .Where(Directory.Exists)
	//	//				 .SelectMany(Directory.EnumerateDirectories)
	//	//				 .SelectMany(d => Directory.EnumerateFiles(d, "*.psd1"))
	//	//				 .Select(Path.GetFileNameWithoutExtension)
	//	//				 .Distinct(StringComparer.OrdinalIgnoreCase)
	//	//				 .ToArray();

	//	//iss.ImportPSModule(allModules);

	//	// 2. 打开 Runspace
	//	using var runspace = RunspaceFactory.CreateRunspace(iss);
	//	runspace.Open();

	//	// 3. 正常跑脚本
	//	var ps = PowerShell.Create();
	//	ps.Runspace = runspace;
	//	return ps;
	//}

	/// <summary>
	/// 返回本机第一个能出网的 IPv4 地址（跳过回环、隧道、虚拟网卡）。
	/// 如果本机没有插网线 / Wi-Fi 没连，会抛异常。
	/// </summary>
	public string GetFirstIpv4()
	{
		try
		{
			return Dns.GetHostEntry(Dns.GetHostName())          // 先拿本机主机名
					  .AddressList
					  .First(ip => ip.AddressFamily == AddressFamily.InterNetwork
								&& !IPAddress.IsLoopback(ip)).ToString();   // 过滤 IPv4 且非 127.x
		}
		catch
		{
			return IPAddress.None.ToString();
		}
	}
}

public class RetryPolicy : IRetryPolicy
{
    private static readonly TimeSpan[] delays = 
    {
        TimeSpan.FromSeconds(0),
        TimeSpan.FromSeconds(2),
        TimeSpan.FromSeconds(10),
        TimeSpan.FromSeconds(30),
    };

    public TimeSpan? NextRetryDelay(RetryContext retryContext)
    {
        // 根据重试次数返回递增的延迟时间，但始终返回非null值确保永久重试
        if (retryContext.PreviousRetryCount < delays.Length)
        {
            return delays[retryContext.PreviousRetryCount];
        }
        
        // 超过预定义次数后，使用固定的30秒间隔
        return TimeSpan.FromSeconds(30);
    }
}
