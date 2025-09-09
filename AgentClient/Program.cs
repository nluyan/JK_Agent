// 1. 配置连接
using Microsoft.AspNetCore.SignalR.Client;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Management;
using System.Management.Automation;
using System.Management.Automation.Runspaces;
using System.Text;
using System.Text.RegularExpressions;

ConcurrentDictionary<string, PowerShell> shells = new();

var connection = new HubConnectionBuilder()
	.WithUrl("http://localhost:5110/AgentHub")
	.WithAutomaticReconnect(new RetryPolicy())
	.Build();

// 添加连接状态监控事件
connection.Closed += async (error) =>
{
	Console.WriteLine($"连接已断开: {error?.Message ?? "未知原因"}");
	if (error != null)
	{
		Console.WriteLine($"错误类型: {error.GetType().Name}");
		Console.WriteLine($"堆栈跟踪: {error.StackTrace}");
	}
	Console.WriteLine("尝试重新连接中...");
};

connection.Reconnecting += (error) =>
{
    Console.WriteLine($"正在重新连接: {error?.Message ?? "连接丢失"}");
    return Task.CompletedTask;
};

connection.Reconnected += async (connectionId) =>
{
    Console.WriteLine($"重连成功，新连接ID: {connectionId}");
    // 重连成功后重新注册代理
    try
    {
        await connection.InvokeAsync("RegisterAgent", GetBoardSerial());
        Console.WriteLine("代理重新注册成功");
    }
    catch (Exception ex)
    {
        Console.WriteLine($"代理重新注册失败: {ex.Message}");
    }
};

connection.On<string>("RegisterTerminal", async terminalId =>
{
	var ps = PowerShell.Create(InitialSessionState.CreateDefault());
	shells.TryAdd(terminalId, ps);

	await connection.SendAsync("PowerShellOutput", terminalId, GetPowerShellPath(ps));
});

connection.On<string, string>("ExecutePowerShell", async (command, terminalId) =>
{
	var ps = shells[terminalId];
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
		output.AppendLine("Critical execution error: " + ex.Message);
	}
	finally
	{
		if (output.Length > 0)
		{
			await connection.SendAsync("PowerShellOutput", terminalId, output.ToString());
		}
		await connection.SendAsync("PowerShellOutput", terminalId, GetPowerShellPath(ps));
	}
});


connection.On<string, string, int>("RequestCompletion", async (terminalId, commandLine, cursorPosition) =>
{
	if (!shells.TryGetValue(terminalId, out var ps))
	{
		await connection.SendAsync("CompletionCallback", terminalId, new List<string>());
		return;
	}

	var completionResult = CommandCompletion.CompleteInput(commandLine, cursorPosition, null, ps);
	var list = completionResult.CompletionMatches.Select(m => m.CompletionText).ToList();
	await connection.SendAsync("CompletionCallback", terminalId, list);
});

connection.On<string, string>("ExecutePowershellScript", async (callId, script) =>
{
	Console.WriteLine($"开始执行PowerShell脚本，CallId: {callId}");

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
			output.AppendLine("Critical execution error: " + ex.Message);
		}

		var outputText = output.ToString();
		Console.WriteLine($"PowerShell执行完成，输出长度: {outputText.Length} 字节");

		// 检查连接状态
		if (connection.State != HubConnectionState.Connected)
		{
			Console.WriteLine($"连接状态异常: {connection.State}，无法发送结果");
			return;
		}
		await connection.SendAsync("PowershellScriptCallback", callId, outputText);
	}
});

string GetPowerShellPath(PowerShell ps)
{
	ps.Commands.Clear();
	ps.Streams.ClearStreams();
	return ps.AddScript("prompt").Invoke<string>().First();
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
    Console.WriteLine("已连接到服务器...");
    await connection.InvokeAsync("RegisterAgent", GetBoardSerial());
    Console.WriteLine("代理注册成功");
}
catch (Exception ex)
{
    Console.WriteLine($"连接失败：{ex.Message}");
    Console.WriteLine("程序将继续运行，等待自动重连...");
}

// 添加连接状态监控循环
var cancellationTokenSource = new CancellationTokenSource();
var monitorTask = Task.Run(async () =>
{
    while (!cancellationTokenSource.Token.IsCancellationRequested)
    {
        await Task.Delay(5000, cancellationTokenSource.Token);
        if (connection.State == HubConnectionState.Disconnected)
        {
            Console.WriteLine("检测到连接断开，尝试重新连接...");
            try
            {
                await connection.StartAsync();
                await connection.InvokeAsync("RegisterAgent", GetBoardSerial());
                Console.WriteLine("手动重连成功");
            }
            catch (Exception ex)
            {
                Console.WriteLine($"手动重连失败: {ex.Message}");
            }
        }
    }
}, cancellationTokenSource.Token);

Console.WriteLine("按任意键退出...");
Console.ReadKey();

cancellationTokenSource.Cancel();
await connection.StopAsync();

public class RetryPolicy : IRetryPolicy
{
	public TimeSpan? NextRetryDelay(RetryContext retryContext)
	{
		// 始终返回2秒间隔，永不停止重连
		return TimeSpan.FromSeconds(2);
	}
}