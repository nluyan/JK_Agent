using AgentClient;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Serilog;
using System.Diagnostics;
using System.Runtime.InteropServices;


// 设置工作目录为应用程序基础目录
var baseDirectory = AppContext.BaseDirectory;
Directory.SetCurrentDirectory(baseDirectory);

if (RuntimeInformation.IsOSPlatform(OSPlatform.Linux))
{
	if(File.Exists("./pwsh/pwsh"))
		Process.Start("chmod", $"+x ./pwsh/pwsh").WaitForExit();

	if (args.Contains("--install"))
	{
		InstallSystemd();
		Console.WriteLine("jike.service is installed.");
		return;
	}
}

Console.WriteLine("Jike Agent 客户端启动中...");

// 创建日志目录
var logDirectory = Path.Combine(baseDirectory, "logs");
Directory.CreateDirectory(logDirectory);

Log.Logger = new LoggerConfiguration()
	//.MinimumLevel.Information()
	.MinimumLevel.Debug()
	.WriteTo.File(Path.Combine(logDirectory, "client_log-.txt"), rollingInterval: RollingInterval.Month) // 每月一个文件
	.WriteTo.Console() // 同时输出到控制台（仅调试模式下可见）
	.CreateLogger();

Log.Information($"应用程序启动，基础目录: {baseDirectory}");
Log.Information($"当前用户: {Environment.UserName}");
Log.Information($"操作系统: {Environment.OSVersion}");
Log.Information($"当前版本: {Settings.Version}");

// 添加全局异常处理器
AppDomain.CurrentDomain.UnhandledException += (sender, e) =>
{
	Log.Error($"未处理的异常: {e.ExceptionObject}");
};

TaskScheduler.UnobservedTaskException += (sender, e) =>
{
	Log.Error(e.Exception, $"未观察到的任务异常: {e.Exception}");
	e.SetObserved();
};

var builder = Host.CreateDefaultBuilder(args);

builder.ConfigureLogging(logging =>
{
	logging.ClearProviders();
});
builder.UseSerilog();
builder.UseWindowsService(options =>
{
	options.ServiceName = "JikeAgent";
});

builder.ConfigureServices(services =>
{
	services.AddHostedService<Worker>();
});

IHost host = builder.Build();
host.Run();

void InstallSystemd()
{
	if (!File.Exists(Path.Combine(Directory.GetCurrentDirectory(), "AgentClient")))
	{
		Console.WriteLine("需要在AgentClient相同目录下执行安装程序");
		Environment.Exit(0);
	}

	var file = "/etc/systemd/system/jike.service";
	if (!File.Exists(file))
	{
		var content = $@"[Unit]
Description=JikeAgent
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory={Directory.GetCurrentDirectory()}
ExecStart={Directory.GetCurrentDirectory()}/AgentClient
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=JikeAgent

[Install]
WantedBy=multi-user.target";
		File.WriteAllText(file, content);

		Process.Start("chmod", $"+x ./pwsh/pwsh").WaitForExit();
		Process.Start("systemctl", "enable jike.service").WaitForExit();
		Process.Start("systemctl", "start jike.service").WaitForExit();
	}
}