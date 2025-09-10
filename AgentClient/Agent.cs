using Microsoft.AspNetCore.SignalR.Client;
using Serilog;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Management;
using System.Management.Automation;
using System.Management.Automation.Runspaces;
using System.Text;
using System.Text.RegularExpressions;

internal class Agent
{
	ConcurrentDictionary<string, PowerShell> shells = new();

	string serverUrl;

	public Agent(string url)
	{
		serverUrl = url;
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
					.WithAutomaticReconnect(new RetryPolicy())
					.Build();

				// 添加连接状态监控事件
				connection.Closed += async (error) =>
				{
					Log.Debug($"连接已断开: {error?.Message ?? "未知原因"}");
					if (error != null)
					{
						Log.Debug($"错误类型: {error.GetType().Name}");
						Log.Debug($"堆栈跟踪: {error.StackTrace}");
					}
					Log.Debug("尝试重新连接中...");
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
						await connection.InvokeAsync("RegisterAgent", GetBoardSerial());
						Log.Debug("代理重新注册成功");
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"代理重新注册失败: {ex.Message}");
					}
				};

				connection.On<string>("RegisterTerminal", async terminalId =>
				{
					try
					{
						var ps = PowerShell.Create(InitialSessionState.CreateDefault());
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

						using (var ps = PowerShell.Create(InitialSessionState.CreateDefault()))
						{
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
					await connection.InvokeAsync("RegisterAgent", GetBoardSerial(), Settings.Version);
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
						while (!cancellationTokenSource.Token.IsCancellationRequested)
						{
							try
							{
								await Task.Delay(5000, cancellationTokenSource.Token);
								if (connection?.State == HubConnectionState.Disconnected)
								{
									Log.Warning("检测到连接断开，尝试重新连接...");
									try
									{
										await connection.StartAsync();
										await connection.InvokeAsync("RegisterAgent", GetBoardSerial());
										Log.Debug("重连成功");
									}
									catch (Exception ex)
									{
										Log.Debug($"重连失败: {ex.Message}");
									}
								}
							}
							catch (TaskCanceledException)
							{
								break;
							}
							catch (Exception ex)
							{
								Log.Error(ex, $"连接监控过程中发生错误: {ex.Message}");
							}
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"连接监控任务异常退出: {ex.Message}");
					}
				}, cancellationTokenSource.Token);

				Log.Debug("代理服务已启动，持续运行中...");

				try
				{
					// 持续运行，直到发生异常
					await Task.Delay(Timeout.Infinite, cancellationTokenSource.Token);
				}
				catch (TaskCanceledException e)
				{
					Log.Error(e, "Agent服务被取消");
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
}

public class RetryPolicy : IRetryPolicy
{
	public TimeSpan? NextRetryDelay(RetryContext retryContext)
	{
		return TimeSpan.FromSeconds(30);
	}
}
