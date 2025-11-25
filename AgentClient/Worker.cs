﻿using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Serilog;
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO.Compression;
using System.Linq;
using System.Text;
using System.Text.Json.Nodes;
using System.Threading.Tasks;

namespace AgentClient
{
	public class Worker : BackgroundService
	{
		string serverUrl = string.Empty;
		string group = string.Empty;

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

			// 启动定期更新检查任务
			_ = Task.Run(async () =>
			{
				var updateCheckInterval = TimeSpan.FromMinutes(10); // 10分钟检查一次
				Log.Debug($"启动定期更新检查，间隔: {updateCheckInterval.TotalMinutes} 分钟");

				while (!stoppingToken.IsCancellationRequested)
				{
					try
					{
						await Task.Delay(updateCheckInterval, stoppingToken);
						Log.Debug("执行定期更新检查...");
						await CheckAndUpdate(serverUrl);
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

			var agent = new Agent($"{serverUrl}/AgentHub", group);
			agent.OnCheckUpdate += async (s, e) =>
			{
				Log.Debug("收到手动更新检查请求...");
				await CheckAndUpdate(serverUrl);
			};
			await agent.Start(stoppingToken);
		}

		bool isCheckingUpdate = false;

		async Task CheckAndUpdate(string serverUrl)
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

					// 下载更新器和新版本
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
					StartUpdater();
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

		void StartUpdater()
		{
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
	}
}
