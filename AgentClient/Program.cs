using System.Diagnostics;
using System.IO.Compression;
using System.Text.Json.Nodes;

if (args.Length == 0)
{
	StartUpdater();
	return;
}

// 添加全局异常处理器
AppDomain.CurrentDomain.UnhandledException += (sender, e) =>
{
	Console.WriteLine($"未处理的异常: {e.ExceptionObject}");
	Console.WriteLine("程序将继续尝试运行...");
};

TaskScheduler.UnobservedTaskException += (sender, e) =>
{
	Console.WriteLine($"未观察到的任务异常: {e.Exception}");
	e.SetObserved(); // 标记为已观察，防止程序崩溃
};

// 主程序无限循环，确保程序永不退出
while (true)
{
	try
	{
		Console.WriteLine("正在启动应用程序...");

		var settings = File.ReadAllText("appsettings.json");
		var node = JsonNode.Parse(settings);
		var serverUrl = node.Root["ServerUrl"]?.ToString();

		if (string.IsNullOrEmpty(serverUrl))
		{
			throw new InvalidOperationException("配置文件中的ServerUrl为空");
		}

		// 启动时检查一次更新
		await CheckAndUpdate(serverUrl);

		// 创建取消令牌源用于控制所有后台任务
		var cancellationTokenSource = new CancellationTokenSource();

		// 启动定期更新检查任务
		var updateCheckTask = Task.Run(async () =>
		{
			var updateCheckInterval = TimeSpan.FromSeconds(10); // 10分钟检查一次
			Console.WriteLine($"启动定期更新检查，间隔: {updateCheckInterval.TotalMinutes} 分钟");

			while (!cancellationTokenSource.Token.IsCancellationRequested)
			{
				try
				{
					await Task.Delay(updateCheckInterval, cancellationTokenSource.Token);
					Console.WriteLine("执行定期更新检查...");
					await CheckAndUpdate(serverUrl);
				}
				catch (TaskCanceledException)
				{
					// 正常取消，忽略异常
					break;
				}
				catch (Exception ex)
				{
					Console.WriteLine($"定期更新检查失败: {ex.Message}");
					// 继续运行，不退出程序
				}
			}
		}, cancellationTokenSource.Token);

		// 启动 Agent 并持续运行
		var agent = new Agent($"{serverUrl}/AgentHub");
		await agent.Start();

		// 如果代码执行到这里，说明Agent.Start()意外结束了
		Console.WriteLine("Agent服务意外结束，程序将重新启动...");

	}
	catch (Exception ex)
	{
		Console.WriteLine($"程序运行过程中发生错误: {ex.Message}");
		Console.WriteLine($"错误详情: {ex}");
		Console.WriteLine("10秒后将重新启动程序...");

		try
		{
			await Task.Delay(10000); // 等待10秒后重试
		}
		catch (Exception delayEx)
		{
			Console.WriteLine($"延时等待异常: {delayEx.Message}");
			// 即使延时失败也要继续
		}
	}
}

async Task CheckAndUpdate(string serverUrl)
{
	try
	{
		if (string.IsNullOrEmpty(serverUrl))
		{
			Console.WriteLine("ServerUrl为空，跳过更新检查");
			return;
		}

		string remoteVersionUrl = $"{serverUrl}/update/version.txt";
		using var httpClient = new HttpClient();
		httpClient.Timeout = TimeSpan.FromSeconds(10); // 设置超时时间

		string remoteVersion = (await httpClient.GetStringAsync(remoteVersionUrl)).Trim();

		if (string.IsNullOrEmpty(remoteVersion))
		{
			Console.WriteLine("远程版本信息为空，跳过更新");
			return;
		}

		if (new Version(remoteVersion) > new Version(Settings.Version))
		{
			Console.WriteLine($"发现新版本: {remoteVersion}，当前版本: {Settings.Version}");
			Console.WriteLine("开始下载更新...");

			// 下载更新器和新版本
			await DownloadFileAsync($"{serverUrl}/update/Updater.exe", "Updater.exe");
			await DownloadFileAsync($"{serverUrl}/update/AgentClient.zip", "AgentClient.zip");
			if(Directory.Exists("temp"))
				Directory.Delete("temp", true);
			ZipFile.ExtractToDirectory("AgentClient.zip", "temp", overwriteFiles: true);

			Console.WriteLine("启动更新程序...");
			// 启动 Updater（主程序退出）
			StartUpdater();
			Environment.Exit(0);
		}
	}
	catch (Exception ex)
	{
		Console.WriteLine($"更新检查失败: {ex.Message}");
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
		httpClient.Timeout = TimeSpan.FromSeconds(30); // 增加下载超时时间
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

		Console.WriteLine($"文件下载成功: {filePath}");
	}
	catch (Exception ex)
	{
		Console.WriteLine($"下载文件失败 {url} -> {filePath}: {ex.Message}");
		throw; // 重新抛出异常，让上层处理
	}
}