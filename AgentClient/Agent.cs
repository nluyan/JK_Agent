using AgentClient;
using Microsoft.AspNetCore.SignalR.Client;
using Microsoft.Extensions.DependencyInjection;
using Serilog;
using System.Diagnostics;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;

internal class Agent
{
	string serverUrl;
	string group;

	public event EventHandler OnCheckUpdate;

	public Agent(string url, string group)
	{
		serverUrl = url;
		this.group = group;
	}
	public async Task Start(CancellationToken stoppingToken)
	{
		HubConnection connection = null;

		// 无限循环确保Agent永不退出
		while (!stoppingToken.IsCancellationRequested)
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
					if (!stoppingToken.IsCancellationRequested)
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
								await RegisterClient();
							}
						}
						catch (Exception ex)
						{
							Log.Warning($"立即重连失败: {ex.Message}，将由监控任务继续尝试");
						}
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
						await RegisterClient();
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"代理重新注册失败: {ex.Message}");
					}
				};

				connection.On<string>("CaptureScreen", async requestId =>
				{
					Log.Information("收到CaptureScreen请求");
					try
					{
						ScreenCapture.StartCapture();
						await Task.Delay(2000);
						if (File.Exists("screenshot.jpg"))
						{
							var data = File.ReadAllBytes("screenshot.jpg");
							await connection.SendAsync("CaptureScreenCallback", requestId, data);
						}
						else
						{
							Log.Error("截图文件不存在");
							await connection.SendAsync("CaptureScreenCallback", requestId, Array.Empty<byte>());
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"截屏失败: {ex.Message}");
					}
				});

				//connection.On<string>("RegisterTerminal", async terminalId =>
				//{
				//	Log.Information("收到RegisterTerminal请求");
				//	try
				//	{
				//		var ps = PowerShell.Create();
				//		shells.TryAdd(terminalId, ps);

				//		if (connection.State == HubConnectionState.Connected)
				//		{
				//			await connection.SendAsync("PowerShellOutput", terminalId, GetPowerShellPath(ps));
				//		}
				//	}
				//	catch (Exception ex)
				//	{
				//		Log.Error(ex, $"注册终端失败 {terminalId}: {ex.Message}");
				//	}
				//});

				//connection.On<string>("TerminalClosed", async terminalId =>
				//{
				//	Log.Information("收到TerminalClosed请求");
				//	try
				//	{
				//		if (shells.TryRemove(terminalId, out var ps))
				//		{
				//			ps.Dispose();
				//		}
				//	}
				//	catch (Exception ex)
				//	{
				//		Log.Error(ex, $"关闭终端失败 {terminalId}: {ex.Message}");
				//	}
				//});

				connection.On("CheckUpdate", () =>
				{
					Log.Information("收到CheckUpdate请求");
					OnCheckUpdate?.Invoke(this, EventArgs.Empty);
				});

				connection.On<string, string, string>("RemoteDesk", async (callId, server, key) =>
				{
					Log.Information("收到RemoteDesk请求");
					try
					{
						var result = await RustDeskIpcUtils.StartRemoteDesk(server, key);
						await connection.SendAsync("RemoteDeskCallback", callId, result);
					}
					catch(Exception ex)
					{
						await connection.SendAsync("RemoteDeskCallback", callId, ex.Message);
					}
					
				});

				connection.On<string, string, string>("InstallRemoteDesk", async (callId, server, key) =>
				{
					Log.Information("收到安装RemoteDesk请求");
					try
					{
						var process = Process.Start("RustDesk.exe", "--silent-install");
						process.WaitForExit();
						await connection.SendAsync("InstallRemoteDeskCallback", callId, "ok");
					}
					catch (Exception ex)
					{
						await connection.SendAsync("InstallRemoteDeskCallback", callId, ex.Message);
					}
				});

				connection.On<string, string>("ExecutePowershellScript", async (callId, script) =>
				{
					Log.Information("收到ExecutePowershellScript请求");
					try
					{
						string outputText = "执行结果为空。";
						Log.Information("执行脚本:\n" + script);
						outputText = ExecuteScriptNatively(script);

						try
						{
							await connection.SendAsync("PowershellScriptCallback", callId, outputText, stoppingToken);
						}
						catch (Exception sendEx)
						{
							Log.Error(sendEx, $"发送PowerShell脚本结果失败: {sendEx.Message}");
						}
					}
					catch (Exception ex)
					{
						Log.Error(ex, $"执行PowerShell脚本失败 {callId}: {ex.Message}");
					}
				});

				string ExecuteScriptNatively(string script)
				{
					var tempDir = System.IO.Path.GetTempPath();
					var fileId = Guid.NewGuid();
					var scriptFile = fileId + ".ps1";
					var scriptPath = System.IO.Path.Combine(tempDir, scriptFile);

					var outPath = System.IO.Path.Combine(tempDir, fileId + ".out.txt");
					var errPath = System.IO.Path.Combine(tempDir, fileId + ".err.txt");

					var wrapperFile = fileId + ".wrapper.ps1";
					var wrapperPath = System.IO.Path.Combine(tempDir, wrapperFile);

					// 写入目标脚本（带 BOM 的 UTF-8）
					System.IO.File.WriteAllText(scriptPath, script, new System.Text.UTF8Encoding(true));

					static string EscapeForSingleQuotedPowerShell(string s) => s?.Replace("'", "''") ?? s;

					// wrapper: 设置编码并将脚本的输出（包括 stderr）写入 outPath，以 UTF8 编码
					var wrapperContent = $@"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8
$OutputEncoding=[System.Text.Encoding]::UTF8
try {{
 & '{EscapeForSingleQuotedPowerShell(scriptPath)}'2>&1 | Out-File -FilePath '{EscapeForSingleQuotedPowerShell(outPath)}' -Encoding UTF8
 exit $LASTEXITCODE
}} catch {{
 $_ | Out-File -FilePath '{EscapeForSingleQuotedPowerShell(errPath)}' -Encoding UTF8
 exit1
}}";

					System.IO.File.WriteAllText(wrapperPath, wrapperContent, new System.Text.UTF8Encoding(true));

					try
					{
						var cmd = "powershell";
						if (RuntimeInformation.IsOSPlatform(OSPlatform.Linux))
							cmd = "./pwsh/pwsh";
						var startInfo = new System.Diagnostics.ProcessStartInfo(cmd, $"-NoProfile -ExecutionPolicy Bypass -File \"{wrapperPath}\"")
						{
							UseShellExecute = false,
							CreateNoWindow = true,
							WorkingDirectory = tempDir
						};

						using var proc = System.Diagnostics.Process.Start(startInfo);
						if (proc == null)
							return "无法启动 PowerShell进程。";

						proc.WaitForExit();

						byte[] ReadIfExists(string p) => System.IO.File.Exists(p) ? System.IO.File.ReadAllBytes(p) : Array.Empty<byte>();

						var outBytes = ReadIfExists(outPath);
						var errBytes = ReadIfExists(errPath);

						string DecodeWithBomAndFallback(byte[] bytes)
						{
							if (bytes == null || bytes.Length == 0) return string.Empty;

							// BOM checks
							if (bytes.Length >= 3 && bytes[0] == 0xEF && bytes[1] == 0xBB && bytes[2] == 0xBF)
								return System.Text.Encoding.UTF8.GetString(bytes, 3, bytes.Length - 3);
							if (bytes.Length >= 2 && bytes[0] == 0xFF && bytes[1] == 0xFE)
								return System.Text.Encoding.Unicode.GetString(bytes, 2, bytes.Length - 2);
							if (bytes.Length >= 2 && bytes[0] == 0xFE && bytes[1] == 0xFF)
								return System.Text.Encoding.BigEndianUnicode.GetString(bytes, 2, bytes.Length - 2);

							// Heuristic: many zero bytes -> likely UTF-16 LE
							int zeroCount = 0;
							for (int i = 0; i < bytes.Length; i++) if (bytes[i] == 0) zeroCount++;
							if (zeroCount > bytes.Length / 4)
							{
								try { return System.Text.Encoding.Unicode.GetString(bytes); } catch { }
							}

							// Try UTF-8 first
							var utf8 = System.Text.Encoding.UTF8.GetString(bytes);
							if (!utf8.Contains('\uFFFD'))
								return utf8;

							// Fallback to ANSI (GBK / CP936)
							try { return System.Text.Encoding.GetEncoding(936).GetString(bytes); } catch { return utf8; }
						}

						var output = DecodeWithBomAndFallback(outBytes);
						var error = DecodeWithBomAndFallback(errBytes);

						if (!string.IsNullOrEmpty(error))
						{
							if (string.IsNullOrEmpty(output)) return error;
							return output + Environment.NewLine + error;
						}

						return output;
					}
					finally
					{
						try { if (System.IO.File.Exists(scriptPath)) System.IO.File.Delete(scriptPath); } catch { }
						try { if (System.IO.File.Exists(wrapperPath)) System.IO.File.Delete(wrapperPath); } catch { }
						try { if (System.IO.File.Exists(outPath)) System.IO.File.Delete(outPath); } catch { }
						try { if (System.IO.File.Exists(errPath)) System.IO.File.Delete(errPath); } catch { }
					}
				}

				string GetUniqeId()
				{
					var mac = GetFirstPhysicalMac();
					if (mac == null)
						return "None";
					else
					{
						byte[] bytes = Encoding.UTF8.GetBytes($"{mac}");
						byte[] hash = MD5.HashData(bytes);
						return Convert.ToHexString(hash).ToLowerInvariant();
					}
				}

				async Task RegisterClient() 
				{
					int platform = RuntimeInformation.IsOSPlatform(OSPlatform.Windows) ? 1 :
						RuntimeInformation.IsOSPlatform(OSPlatform.Linux) ? 2 :
						RuntimeInformation.IsOSPlatform(OSPlatform.OSX) ? 3 : 0;
					string osArch = RuntimeInformation.OSArchitecture.ToString();
					string osDesc = RuntimeInformation.OSDescription;
					await connection.InvokeAsync("RegisterAgent",
						GetUniqeId(),
						Settings.Version,
						GetAllIP(),
						group,
						platform,
						osArch,
						osDesc,
						stoppingToken);
					Log.Information("代理注册成功");
				}

				string? GetFirstPhysicalMac()
				{
					try
					{
						var nic = NetworkInterface.GetAllNetworkInterfaces()
									.OrderBy(n => n.GetPhysicalAddress().ToString() == "" ? 1 : 0)
									.ThenBy(n => n.Id)
									.FirstOrDefault(n =>
										n.OperationalStatus == OperationalStatus.Up &&
										n.NetworkInterfaceType != NetworkInterfaceType.Loopback &&
										!n.Description.ToLowerInvariant().Contains("virtual") &&
										!n.Description.ToLowerInvariant().Contains("vmware") &&
										!n.Description.ToLowerInvariant().Contains("hyper-v"));

						var mac = nic?.GetPhysicalAddress().ToString();
						if (!string.IsNullOrWhiteSpace(mac) && mac.Length == 12)
							return mac; // 例：E41D2D3A4B5C
					}
					catch (Exception ex)
					{

					}
					return null;
				}

				while (!stoppingToken.IsCancellationRequested)
				{
					try
					{
						Log.Debug($"尝试连接到服务器: {serverUrl}");
						await connection.StartAsync(stoppingToken);
						Log.Information("已连接到服务器...");
						await RegisterClient();
						break;
					}
					catch (Exception ex)
					{
						Log.Error($"连接失败：{ex.Message}");
						await Task.Delay(5000);
					}
				}

				Log.Information("Agent服务已启动，持续运行中...");

				try
				{
					// 持续运行，直到发生异常或被取消
					await Task.Delay(Timeout.Infinite, stoppingToken);
				}
				catch (TaskCanceledException)
				{
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
						if (connection != null)
						{
							await connection.StopAsync(stoppingToken);
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
				await Task.Delay(5000, stoppingToken); // 等待5秒后重试
													   // 继续循环，重新启动Agent
			}
		}
	}

	public string GetAllIP()
	{
		try
		{
			return string.Join(',', Dns.GetHostEntry(Dns.GetHostName())
					  .AddressList
					  .Where(ip => ip.AddressFamily == AddressFamily.InterNetwork
								&& !IPAddress.IsLoopback(ip))
					  .Select(c => c.ToString()));   // 过滤 IPv4 且非 127.x
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
