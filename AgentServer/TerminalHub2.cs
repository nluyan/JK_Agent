//using Microsoft.AspNetCore.SignalR;
//using System.Collections.Concurrent;
//using System.Management.Automation;
//using System.Management.Automation.Runspaces;
//using System.Text;

//namespace AgentServer // Or your actual namespace like AgentServer.Hubs
//{
//	public class TerminalHub : Hub
//	{
//		private static readonly ConcurrentDictionary<string, PowerShell> PSHInstances = new();

//		public override Task OnConnectedAsync()
//		{
//			var connectionId = Context.ConnectionId;
//			var ps = PowerShell.Create(InitialSessionState.CreateDefault());

//			// Associate the streams with handlers BEFORE any invocation
//			// The output from ps.InvokeAsync below will be routed to these handlers
//			ps.Streams.Error.DataAdded += (sender, args) =>
//			{
//				var errorRecord = ((PSDataCollection<ErrorRecord>)sender!)[args.Index];
//				Clients.Client(connectionId).SendAsync("ReceiveOutput", "\nError: " + errorRecord.ToString());
//			};

//			ps.Streams.Information.DataAdded += (sender, args) =>
//			{
//				var infoRecord = ((PSDataCollection<InformationRecord>)sender!)[args.Index];
//				Clients.Client(connectionId).SendAsync("ReceiveOutput", "\n" + infoRecord.MessageData.ToString());
//			};

//			PSHInstances[connectionId] = ps;

//			// Immediately send a prompt to the client so the user knows it's ready
//			Clients.Client(connectionId).SendAsync("ReceiveOutput", "PS> ");

//			return base.OnConnectedAsync();
//		}

//		public async Task SendInput(string command)
//		{
//			var connectionId = Context.ConnectionId;
//			if (PSHInstances.TryGetValue(connectionId, out var ps))
//			{
//				// Prevent concurrent execution on the same PowerShell instance
//				if (ps.InvocationStateInfo.State == PSInvocationState.Running)
//				{
//					return;
//				}

//				ps.Commands.Clear(); // Clear any previous commands
//				ps.AddScript(command);
//				ps.AddCommand("Out-String").AddParameter("Stream");

//				try
//				{
//					// *** THE FIX IS HERE ***
//					// 1. Capture the results from InvokeAsync
//					var results = await ps.InvokeAsync();

//					// 2. Process the results and send them to the client
//					if (results.Count > 0)
//					{
//						var outputString = new StringBuilder();
//						foreach (var psObject in results)
//						{
//							// Append the string representation of each output object
//							outputString.AppendLine(psObject.ToString());
//						}

//						// Send the collected output as a single message
//						if (outputString.Length > 0)
//						{
//							await Clients.Client(connectionId).SendAsync("ReceiveOutput", outputString.ToString());
//						}
//					}
//				}
//				catch (Exception ex)
//				{
//					await Clients.Client(connectionId).SendAsync("ReceiveOutput", "\nCritical Error: " + ex.Message);
//				}
//				finally
//				{
//					// After the command is done, send a new prompt.
//					await Clients.Client(connectionId).SendAsync("ReceiveOutput", "PS> ");
//				}
//			}
//		}

//		public override Task OnDisconnectedAsync(Exception? exception)
//		{
//			var connectionId = Context.ConnectionId;
//			if (PSHInstances.TryRemove(connectionId, out var ps))
//			{
//				ps.Dispose();
//			}
//			return base.OnDisconnectedAsync(exception);
//		}
//	}
//}