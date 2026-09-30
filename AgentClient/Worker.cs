﻿using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Serilog;
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO.Compression;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json.Nodes;
using System.Threading.Tasks;

namespace AgentClient
{
	public class Worker : BackgroundService
	{
		string serverUrl = string.Empty;
		string group = string.Empty;
		string hubAuthToken = string.Empty;
		int startupJitterSeconds = Settings.DefaultStartupJitterSeconds;
		int updateJitterSeconds = Settings.DefaultUpdateJitterSeconds;

		protected override async Task ExecuteAsync(CancellationToken stoppingToken)
		{
			Log.Debug("正在启动应用程序...");
			
			// 确保工作目录正确
			var baseDirectory = AppContext.BaseDirectory;
			Log.Debug($"应用程序基础目录: {baseDirectory}");
			Directory.SetCurrentDirectory(baseDirectory);
			
			// 读取配置文件
			var configPath = Path.Combine(baseDirectory, "appsettings.json");
			Log.Debug($"配置文件路径: {configPath}");
			
			if (!File.Exists(configPath))
			{
				Log.Error($"配置文件不存在: {configPath}");
				throw new FileNotFoundException($"配置文件不存在: {configPath}");
			}
			
			var settings = File.ReadAllText(configPath);
			var node = JsonNode.Parse(settings);
			serverUrl = node.Root["ServerUrl"]?.ToString();
			group = node.Root["Group"]?.ToString();
			hubAuthToken = node.Root["HubAuthToken"]?.ToString() ?? string.Empty;
			
			if (string.IsNullOrEmpty(serverUrl))
			{
				Log.Error("配置文件中的ServerUrl为空或无效");
				throw new InvalidOperationException("配置文件中的ServerUrl为空或无效");
			}
			
			if (string.IsNullOrEmpty(group))
			{
				Log.Error("配置文件中的Group为空或无效");
				throw new InvalidOperationException("配置文件中的Group为空或无效");
			}
			
			Log.Information($"配置加载成功 - ServerUrl: {serverUrl}, Group: {group}");

			// 错峰配置：启动错峰避免大批Agent同时连接，升级错峰避免同时拉取升级包打满服务端带宽
			startupJitterSeconds = ReadJitterSeconds(node.Root["StartupJitter"], Settings.DefaultStartupJitterSeconds);
			updateJitterSeconds = ReadJitterSeconds(node.Root["UpdateJitter"], Settings.DefaultUpdateJitterSeconds);
			Log.Information($"错峰配置 - 启动错峰上限: {startupJitterSeconds}秒, 升级错峰上限: {updateJitterSeconds}秒");

			// 只提示令牌是否已配置，不打印令牌内容，避免凭据落进日志
			Log.Information(hubAuthToken.Length > 0 ? "Hub鉴权令牌已配置" : "未配置HubAuthToken，将以无令牌方式连接AgentHub");

			// 启动错峰：随机延迟后再连接服务器并开始检查更新，避免大批Agent同时冲击服务端
			if (startupJitterSeconds > 0)
			{
				var startupDelay = TimeSpan.FromSeconds(Random.Shared.Next(startupJitterSeconds + 1));
				Log.Information($"启动错峰，{startupDelay.TotalSeconds:0}秒后连接服务器");
				try
				{
					await Task.Delay(startupDelay, stoppingToken);
				}
				catch (TaskCanceledException)
				{
					// 服务正在停止，直接退出
					return;
				}
			}

			// 启动定期更新检查任务
			_ = Task.Run(async () =>
			{
				var checkUpdate = node.Root["CheckUpdate"]?.ToString();
				var updateCheckInterval = checkUpdate == null ? TimeSpan.FromMinutes(10) : TimeSpan.FromSeconds(int.Parse(checkUpdate));
				Log.Debug($"启动定期更新检查，间隔: {updateCheckInterval.TotalMinutes} 分钟");

				while (!stoppingToken.IsCancellationRequested)
				{
					try
					{
						// 每轮间隔叠加抖动，避免长期运行后所有Agent的检查请求对齐
						await Task.Delay(JitterInterval(updateCheckInterval), stoppingToken);
						//Log.Debug("执行定期更新检查...");
						await CheckAndUpdate(serverUrl, stoppingToken);
					}
					catch (TaskCanceledException)
					{
						// 正常取消，忽略异常
						break;
					}
					catch (Exception ex)
					{
						Log.Debug(ex, $"定期更新检查失败: {ex.Message}");
						// 继续运行，不退出程序
					}
				}
			}, stoppingToken);

			var agent = new Agent($"{serverUrl}/AgentHub", group, hubAuthToken);
			agent.OnCheckUpdate += async (s, e) =>
			{
				Log.Debug("收到手动更新检查请求...");
				await CheckAndUpdate(serverUrl, stoppingToken);
			};
			await agent.Start(stoppingToken);
		}

		bool isCheckingUpdate = false;

		async Task CheckAndUpdate(string serverUrl, CancellationToken stoppingToken)
		{
			if(isCheckingUpdate)
			{
				return;
			}
			isCheckingUpdate = true;
			try
			{
				if (string.IsNullOrEmpty(serverUrl))
				{
					Log.Debug("ServerUrl为空，跳过更新检查");
					return;
				}

				string remoteVersionUrl = $"{serverUrl}/update/{group}/version.txt";
				using var httpClient = new HttpClient();
				httpClient.Timeout = TimeSpan.FromSeconds(10); // 设置超时时间

				string remoteVersion = (await httpClient.GetStringAsync(remoteVersionUrl)).Trim();

				if (string.IsNullOrEmpty(remoteVersion))
				{
					Log.Debug("远程版本信息为空，跳过更新");
					return;
				}

				if (new Version(remoteVersion) > new Version(Settings.Version))
				{
					Log.Information($"发现新版本: {remoteVersion}，当前版本: {Settings.Version} 开始下载更新...");

					// 升级错峰：随机延迟后再下载，避免大批Agent同时拉取升级包打满服务端带宽
					if (updateJitterSeconds > 0)
					{
						var jitterDelay = TimeSpan.FromSeconds(Random.Shared.Next(updateJitterSeconds + 1));
						Log.Information($"升级错峰，{jitterDelay.TotalSeconds:0}秒后开始下载更新");
						try
						{
							await Task.Delay(jitterDelay, stoppingToken);
						}
						catch (TaskCanceledException)
						{
							// 服务正在停止，放弃本次更新
							return;
						}

						// 等待期间服务端版本可能已经变化，下载前重新确认一次
						remoteVersion = (await httpClient.GetStringAsync(remoteVersionUrl)).Trim();
						if (string.IsNullOrEmpty(remoteVersion) || new Version(remoteVersion) <= new Version(Settings.Version))
						{
							Log.Information($"升级错峰等待后无需更新，远程版本: {remoteVersion}");
							return;
						}
					}

					// 下载更新器和新版本
					if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
					{
						await DownloadFileAsync($"{serverUrl}/update/{group}/Updater.exe", "Updater.exe");
						await DownloadFileAsync($"{serverUrl}/update/{group}/AgentClient.zip", "AgentClient.zip");
						Log.Information("删除旧的临时目录...");
						if (Directory.Exists("temp"))
							Directory.Delete("temp", true);
						Log.Information("解压更新包...");
						ZipFile.ExtractToDirectory("AgentClient.zip", "temp", overwriteFiles: true);
						Log.Information("删除更新包...");
						File.Delete("AgentClient.zip");
						Log.Information("启动更新程序...");
						try
						{
							Process.Start(new ProcessStartInfo
							{
								FileName = "Updater.exe",
								UseShellExecute = true,
								CreateNoWindow = true,
								WorkingDirectory = AppDomain.CurrentDomain.BaseDirectory,
								WindowStyle = ProcessWindowStyle.Hidden
							});
						}
						catch (Exception ex)
						{
							Log.Error(ex, $"Updater.exe执行失败: {ex.Message}");
						}
					}
					else
					{
						//await DownloadFileAsync($"{serverUrl}/update/{group}/Updater.sh", "Updater.sh");
						await DownloadFileAsync($"{serverUrl}/update/{group}/AgentClient.zip", "AgentClient.zip");
						Log.Information("删除旧的临时目录...");
						if (Directory.Exists("temp"))
							Directory.Delete("temp", true);
						Log.Information("解压更新包...");
						ZipFile.ExtractToDirectory("AgentClient.zip", "temp", overwriteFiles: true);
						Log.Information("删除更新包...");
						File.Delete("AgentClient.zip");
						Log.Information("复制文件...");
						CopyDirectory("temp", ".");
						Log.Information("退出应用");
						Process.Start("chmod", "+x AgentClient").WaitForExit();
						Environment.Exit(0);
					}
				}
			}
			catch (Exception ex)
			{
				Log.Error(ex, $"更新检查失败: {ex.Message}");
			}
			finally
			{
				isCheckingUpdate = false;
			}
		}

		void CopyDirectory(string src, string dst, bool overwrite = true)
		{
			if (src == null) throw new ArgumentNullException(nameof(src));
			if (dst == null) throw new ArgumentNullException(nameof(dst));

			// 1.  normalize 路径，支持 "..\xxx" 这种相对路径
			var srcDir = new DirectoryInfo(Path.GetFullPath(src));
			var dstDir = new DirectoryInfo(Path.GetFullPath(dst));

			if (!srcDir.Exists)
				throw new DirectoryNotFoundException($"源目录不存在: {srcDir.FullName}");

			// 2.  递归拷贝
			CopyRecursive(srcDir, dstDir, overwrite);
		}

		void CopyRecursive(DirectoryInfo src, DirectoryInfo dst, bool overwrite)
		{
			// 保证目标目录存在
			if (!dst.Exists)
				dst.Create();

			// 先拷文件
			foreach (var file in src.GetFiles())
			{
				var dstFile = new FileInfo(Path.Combine(dst.FullName, file.Name));
				try
				{
					//file.CopyTo(dstFile.FullName, overwrite);
					//file.MoveTo(dstFile.FullName, true);
					Console.WriteLine("mv " + $"{file} {dstFile.FullName}");
					Process.Start("mv", $"{file} {dstFile.FullName}");
				}
				catch (Exception ex)
				{
					File.AppendAllText(Path.Combine(Directory.GetCurrentDirectory(), "logs", "update_log.txt"), ex.Message + "\n");
				}
			}

			// 再递归子目录
			foreach (var subDir in src.GetDirectories())
			{
				var dstSub = new DirectoryInfo(Path.Combine(dst.FullName, subDir.Name));
				CopyRecursive(subDir, dstSub, overwrite);
			}
		}

		async Task DownloadFileAsync(string url, string filePath)
		{
			try
			{
				if (string.IsNullOrEmpty(url) || string.IsNullOrEmpty(filePath))
				{
					throw new ArgumentException("URL或文件路径不能为空");
				}

				using var httpClient = new HttpClient();
				httpClient.Timeout = TimeSpan.FromMinutes(10);
				using var response = await httpClient.GetAsync(url, HttpCompletionOption.ResponseHeadersRead);
				response.EnsureSuccessStatusCode();

				var directory = Path.GetDirectoryName(filePath);
				if (!string.IsNullOrEmpty(directory))
				{
					Directory.CreateDirectory(directory);
				}

				using var contentStream = await response.Content.ReadAsStreamAsync();
				using var fileStream = new FileStream(filePath, FileMode.Create, FileAccess.Write, FileShare.None);
				await contentStream.CopyToAsync(fileStream);

				Log.Information($"文件下载成功: {filePath}");
			}
			catch (Exception ex)
			{
				Log.Error(ex, $"下载文件失败 {url} -> {filePath}: {ex.Message}");
				throw; // 重新抛出异常，让上层处理
			}
		}

		// 读取错峰配置（秒）：未配置时使用默认值，负数表示关闭错峰
		static int ReadJitterSeconds(JsonNode? node, int defaultValue)
		{
			var raw = node?.ToString();
			if (string.IsNullOrWhiteSpace(raw) || !int.TryParse(raw, out var value))
				return defaultValue;
			return value < 0 ? 0 : value;
		}

		// 给检查间隔叠加 ±30% 抖动，避免长期运行后所有Agent的检查请求对齐
		static TimeSpan JitterInterval(TimeSpan interval)
		{
			if (interval <= TimeSpan.Zero)
				return interval;
			var span = interval.TotalMilliseconds * 0.3;
			var offset = Random.Shared.NextDouble() * 2 * span - span;
			return TimeSpan.FromMilliseconds(Math.Max(0, interval.TotalMilliseconds + offset));
		}
	}
}
