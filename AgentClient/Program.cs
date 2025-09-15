using AgentClient;
using Microsoft.PowerShell;
using Serilog;
using System.Diagnostics;
using System.IO.Compression;
using System.Management.Automation.Internal;
using System.Management.Automation.Runspaces;
using System.Reflection;
using System.Text.Json.Nodes;


AutoStart.SetAutoStart();
if (args.Length == 0)
{
	StartUpdater();
	return;
}

// 1. 一步到位配置
Log.Logger = new LoggerConfiguration()
	.MinimumLevel.Information()
	//.MinimumLevel.Debug()
	.WriteTo.File("logs\\client_log-.txt", rollingInterval: RollingInterval.Month) // 每月一个文件
	.WriteTo.Console() // 同时输出到控制台
	.CreateLogger();

// 添加全局异常处理器
AppDomain.CurrentDomain.UnhandledException += (sender, e) =>
{
	Log.Debug($"未处理的异常: {e.ExceptionObject}");
	Log.Debug("程序将继续尝试运行...");
};

TaskScheduler.UnobservedTaskException += (sender, e) =>
{
	Log.Debug($"未观察到的任务异常: {e.Exception}");
	e.SetObserved(); // 标记为已观察，防止程序崩溃
};

Log.Debug("正在启动应用程序...");
var settings = File.ReadAllText("appsettings.json");
var node = JsonNode.Parse(settings);
var serverUrl = node.Root["ServerUrl"].ToString();
var group = node.Root["Group"].ToString();

if (string.IsNullOrEmpty(serverUrl))
{
	throw new InvalidOperationException("配置文件中的ServerUrl为空");
}
if (string.IsNullOrEmpty(group))
{
	throw new InvalidOperationException("配置文件中的Group为空");
}

// 启动时检查一次更新
await CheckAndUpdate(serverUrl);

// 主程序无限循环，确保程序永不退出
while (true)
{
	try
	{
		// 创建取消令牌源用于控制所有后台任务
		var cancellationTokenSource = new CancellationTokenSource();

		// 启动定期更新检查任务
		var updateCheckTask = Task.Run(async () =>
		{
			var updateCheckInterval = TimeSpan.FromMinutes(10); // 10分钟检查一次
			Log.Debug($"启动定期更新检查，间隔: {updateCheckInterval.TotalMinutes} 分钟");

			while (!cancellationTokenSource.Token.IsCancellationRequested)
			{
				try
				{
					await Task.Delay(updateCheckInterval, cancellationTokenSource.Token);
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
		}, cancellationTokenSource.Token);

		// 启动 Agent 并持续运行
		var agent = new Agent($"{serverUrl}/AgentHub", group);
		await agent.Start();

		// 如果代码执行到这里，说明Agent.Start()意外结束了
		Log.Warning("Agent服务意外结束，程序将重新启动...");

	}
	catch (Exception ex)
	{
		Log.Error(ex, $"程序运行过程中发生错误: {ex.Message} 10秒后将重新启动程序...");
		await Task.Delay(10000); // 等待10秒后重试
	}
}

async Task CheckAndUpdate(string serverUrl)
{
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
			if(Directory.Exists("temp"))
				Directory.Delete("temp", true);
			ZipFile.ExtractToDirectory("AgentClient.zip", "temp", overwriteFiles: true);

			Log.Information("启动更新程序...");
			// 启动 Updater（主程序退出）
			StartUpdater();
			Environment.Exit(0);
		}
	}
	catch (Exception ex)
	{
		Log.Error(ex, $"更新检查失败: {ex.Message}");
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
	catch { }
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