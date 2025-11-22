﻿using AgentClient;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Hosting.WindowsServices;
using Microsoft.Extensions.Logging;
using Microsoft.PowerShell;
using Microsoft.Win32;
using Serilog;
using System.Diagnostics;
using System.IO.Compression;
using System.Management.Automation.Internal;
using System.Management.Automation.Runspaces;
using System.Reflection;
using System.Text.Json.Nodes;

//删除老版本
//foreach(var p in Process.GetProcessesByName("AgentClient"))
//{
//	if (p.Id != Process.GetCurrentProcess().Id)
//	{
//		p.Kill();
//	}
//}
//DeleteRegisterTable();

Console.WriteLine("Jike Agent 客户端启动中...");

// 设置工作目录为应用程序基础目录
var baseDirectory = AppContext.BaseDirectory;
Directory.SetCurrentDirectory(baseDirectory);

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


//void DeleteRegisterTable()
//{
//	const string keyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";
//	const string valueName = "JK_Agent_Client";

//	try
//	{
//		// 打开 HKCU\...\Run 键，要求“可写”
//		using (RegistryKey runKey = Registry.CurrentUser.OpenSubKey(keyPath, true))
//		{
//			// 判断值是否存在
//			if (runKey.GetValue(valueName) == null)
//			{
//				Console.WriteLine("JK_Agent_Client 不存在，无需删除。");
//			}
//			else
//			{
//				runKey.DeleteValue(valueName, false);   // false = 不抛异常（如果刚被别的进程删了）
//				Console.WriteLine("JK_Agent_Client 已成功删除。");
//			}
//		}
//	}
//	catch (Exception ex)
//	{
//	}
//}