// 1. 配置连接
using Microsoft.AspNetCore.SignalR.Client;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.Management.Automation;
using System.Management.Automation.Runspaces;
using System.Text;

ConcurrentDictionary<string, PowerShell> shells = new();

var connection = new HubConnectionBuilder()
	.WithUrl("http://localhost:5110/AgentHub")
	.WithAutomaticReconnect()
	.Build();

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


string GetPowerShellPath(PowerShell ps)
{
	ps.Commands.Clear();
	ps.Streams.ClearStreams();
	return ps.AddScript("prompt").Invoke<string>().First();
}

try
{
	await connection.StartAsync();
	Console.WriteLine("已连接到服务器...");
	await connection.InvokeAsync("RegisterAgent");
}
catch (Exception ex)
{
	Console.WriteLine($"连接失败：{ex.Message}");
	return;
}

Console.WriteLine("按任意键退出...");
Console.ReadKey();

await connection.StopAsync();