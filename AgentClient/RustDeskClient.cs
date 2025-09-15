using System.Buffers.Binary;
using System.Data;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;
namespace AgentClient
{


#nullable enable

	// --- IPC Request/Response classes ---

	public abstract class IpcRequest
	{
		[JsonPropertyName("t")]
		public abstract string Type { get; }
	}

	public class SystemInfoRequest : IpcRequest
	{
		public override string Type => "SystemInfo";
		[JsonPropertyName("c")]
		[JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
		public string? Content { get; set; } = null;
	}

	public class ConfigRequest : IpcRequest
	{
		public override string Type => "Config";
		[JsonPropertyName("c")]
		public object?[] Content { get; set; }

		public ConfigRequest(string key, string? value = null)
		{
			Content = new object?[] { key, value };
		}
	}

	public class OptionsRequest : IpcRequest
	{
		public override string Type => "Options";
		[JsonPropertyName("c")]
		[JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
		public Dictionary<string, string>? Content { get; set; }
	}

	public class CloseRequest : IpcRequest
	{
		public override string Type => "Close";
	}

	// --- Response Data Structures ---

	public class OnlineStatus
	{
		public long Timestamp { get; set; }
		public bool IsOnline { get; set; }
	}


	/// <summary>
	/// Handles communication with the RustDesk IPC server over a named pipe.
	/// </summary>
	public class RustDeskClient
	{
		// RustDesk's default IPC pipe name on Windows
		private const string PipeName = @"RustDesk\query";

		private static readonly JsonSerializerOptions s_jsonOptions = new()
		{
			PropertyNamingPolicy = null,
			DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
		};

		/// <summary>
		/// Sends a request to the RustDesk IPC and returns the response.
		/// </summary>
		public async Task<JsonNode?> SendRequestAsync(object request, int timeoutMs = 2000)
		{
			await using var pipeClient = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut);

			try
			{
				await pipeClient.ConnectAsync(timeoutMs);
			}
			catch (TimeoutException)
			{
				Console.WriteLine("Error: Connection to RustDesk IPC timed out.");
				Console.WriteLine("Please ensure the main RustDesk application is running.");
				return null;
			}

			using var cts = new CancellationTokenSource(timeoutMs);

			// --- Write Request ---
			var requestJson = JsonSerializer.Serialize(request, s_jsonOptions);
			var requestBytes = Encoding.UTF8.GetBytes(requestJson);
			var lengthPrefix = RustDeskIpcUtils.CreateLengthPrefix(requestBytes.Length);

			await pipeClient.WriteAsync(lengthPrefix, cts.Token);
			await pipeClient.WriteAsync(requestBytes, cts.Token);
			await pipeClient.FlushAsync(cts.Token);

			// --- Read Response ---
			try
			{
				var firstByteBuff = new byte[1];
				await pipeClient.ReadExactlyAsync(firstByteBuff, 0, 1, cts.Token);
				var firstByte = firstByteBuff[0];
				var headerLen = (firstByte & 0x3) + 1;

				var headerBuff = new byte[headerLen];
				headerBuff[0] = firstByte;
				if (headerLen > 1)
				{
					await pipeClient.ReadExactlyAsync(headerBuff, 1, headerLen - 1, cts.Token);
				}

				uint rawLength = 0;
				switch (headerLen)
				{
					case 1: rawLength = headerBuff[0]; break;
					case 2: rawLength = BitConverter.ToUInt16(headerBuff, 0); break;
					case 3: rawLength = headerBuff[0] | (uint)headerBuff[1] << 8 | (uint)headerBuff[2] << 16; break;
					case 4: rawLength = BitConverter.ToUInt32(headerBuff, 0); break;
				}

				var responseLength = (int)(rawLength >> 2);
				if (responseLength == 0) return null;

				var responseBuffer = new byte[responseLength];
				await pipeClient.ReadExactlyAsync(responseBuffer, 0, responseLength, cts.Token);
				var responseJson = Encoding.UTF8.GetString(responseBuffer);
				return JsonNode.Parse(responseJson);
			}
			catch (OperationCanceledException)
			{
				Console.WriteLine("Error: Timed out waiting for a response from RustDesk.");
				return null;
			}
			catch (EndOfStreamException)
			{
				Console.WriteLine("Error: The IPC pipe was closed unexpectedly while reading the response.");
				return null;
			}
		}

		public async Task SendRequestWithoutResponseAsync(object request, int timeoutMs = 2000)
		{
			await using var pipeClient = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut);

			try
			{
				await pipeClient.ConnectAsync(timeoutMs);
			}
			catch (TimeoutException)
			{
				Console.WriteLine("Error: Connection to RustDesk IPC timed out.");
				Console.WriteLine("Please ensure the main RustDesk application is running.");
			}

			using var cts = new CancellationTokenSource(timeoutMs);

			// --- Write Request ---
			var requestJson = JsonSerializer.Serialize(request, s_jsonOptions);
			var requestBytes = Encoding.UTF8.GetBytes(requestJson);
			var lengthPrefix = RustDeskIpcUtils.CreateLengthPrefix(requestBytes.Length);

			await pipeClient.WriteAsync(lengthPrefix, cts.Token);
			await pipeClient.WriteAsync(requestBytes, cts.Token);
			await pipeClient.FlushAsync(cts.Token);
		}

		#region Configuration Management

		private async Task<string?> GetConfigValueAsync(string key)
		{
			var response = await SendRequestAsync(new ConfigRequest(key));
			return response?["c"]?[1]?.GetValue<string>();
		}

		private async Task SetConfigValueAsync(string key, string value)
		{
			await SendRequestWithoutResponseAsync(new ConfigRequest(key, value));
		}

		public Task<string?> GetIdAsync() => GetConfigValueAsync("id");
		public Task<string?> GetTemporaryPasswordAsync() => GetConfigValueAsync("temporary-password");
		public Task<string?> GetPermanentPasswordAsync() => GetConfigValueAsync("permanent-password");
		public Task SetPermanentPasswordAsync(string password) => SetConfigValueAsync("permanent-password", password);
		public Task SetTemporaryPasswordAsync(string password) => SetConfigValueAsync("temporary-password", password);
		public Task<string?> GetFingerprintAsync() => GetConfigValueAsync("fingerprint");
		public Task<string?> GetRendezvousServerAsync() => GetConfigValueAsync("rendezvous_server");

		public async Task<Dictionary<string, string>?> GetOptionsAsync()
		{
			var response = await SendRequestAsync(new OptionsRequest());
			return response?["c"]?.Deserialize<Dictionary<string, string>>();
		}

		public async Task SetOptionsAsync(Dictionary<string, string> options)
		{
			await SendRequestAsync(new OptionsRequest { Content = options });
		}

		#endregion

		#region Status and Information

		public async Task<OnlineStatus?> GetOnlineStatusAsync()
		{
			var response = await SendRequestAsync(new { t = "OnlineStatus" });
			var content = response?["c"];
			if (content is JsonArray arr && arr.Count == 2)
			{
				return new OnlineStatus
				{
					Timestamp = arr[0]!.GetValue<long>(),
					IsOnline = arr[1]!.GetValue<bool>()
				};
			}
			return null;
		}

		/// <summary>
		/// Gets the detailed connection status of the RustDesk client.
		/// </summary>
		/// <returns>A ConnectionStatus object containing detailed connection information, or null if the request failed.</returns>
		public async Task<ConnectionStatus?> GetConnectionStatusAsync()
		{
			var response = await SendRequestAsync(new { t = "OnlineStatus" });
			var content = response?["c"];

			if (content is JsonArray arr && arr.Count == 2)
			{
				// Get additional information
				//var id = await GetIdAsync() ?? string.Empty;

				return new ConnectionStatus
				{
					StatusNum = arr[0]!.GetValue<long>() > 0 ? 1 : (arr[0]!.GetValue<long>() == 0 ? 0 : -1),
					KeyConfirmed = arr[1]!.GetValue<bool>(),
					//MouseTime = await GetMouseMoveTimeAsync() ?? 0,
					//Id = id,
					//VideoConnCount = await GetControlledSessionCountAsync() ?? 0
				};
			}

			return null;
		}

		/// <summary>
		/// Checks if RustDesk is currently connected to any remote sessions.
		/// </summary>
		/// <returns>True if connected, false otherwise.</returns>
		public async Task<bool> IsConnectedAsync()
		{
			var status = await GetConnectionStatusAsync();
			return status?.IsConnected ?? false;
		}

		/// <summary>
		/// Checks if there are active remote sessions.
		/// </summary>
		/// <returns>True if there are active sessions, false otherwise.</returns>
		public async Task<bool> HasActiveSessionsAsync()
		{
			var status = await GetConnectionStatusAsync();
			return status?.HasActiveSessions ?? false;
		}

		public async Task<int?> GetNatTypeAsync()
		{
			var response = await SendRequestAsync(new { t = "NatType" });
			return response?["c"]?.GetValue<int>();
		}

		public async Task<long?> GetClickTimeAsync()
		{
			var response = await SendRequestAsync(new { t = "ClickTime" });
			return response?["c"]?.GetValue<long>();
		}

		public async Task<long?> GetMouseMoveTimeAsync()
		{
			var response = await SendRequestAsync(new { t = "MouseMoveTime" });
			return response?["c"]?.GetValue<long>();
		}

		public async Task<int?> GetControlledSessionCountAsync()
		{
			var response = await SendRequestAsync(new { t = "ControlledSessionCount" });
			return response?["c"]?.GetValue<int>();
		}

		#endregion

		#region Application Control

		public async Task<string?> GetSystemInfoAsync()
		{
			var response = await SendRequestAsync(new SystemInfoRequest());
			return response?["c"]?.GetValue<string>();
		}

		public async Task CloseRustDeskAsync()
		{
			await SendRequestAsync(new CloseRequest());
		}

		#endregion
	}

	/// <summary>
	/// Provides helper methods that match RustDesk's custom IPC message framing.
	/// </summary>
	public static class RustDeskIpcUtils
	{
		public static byte[] CreateLengthPrefix(int length)
		{
			if (length < 0) throw new ArgumentOutOfRangeException(nameof(length), "Length cannot be negative.");

			if (length <= 0x3F) return new[] { (byte)(length << 2) };
			if (length <= 0x3FFF)
			{
				var bytes = new byte[2];
				BinaryPrimitives.WriteUInt16LittleEndian(bytes, (ushort)((length << 2) | 0x1));
				return bytes;
			}
			if (length <= 0x3FFFFF)
			{
				uint header = (uint)((length << 2) | 0x2);
				return new[] { (byte)header, (byte)(header >> 8), (byte)(header >> 16) };
			}
			if (length <= 0x3FFFFFFF)
			{
				var bytes = new byte[4];
				BinaryPrimitives.WriteUInt32LittleEndian(bytes, (uint)((length << 2) | 0x3));
				return bytes;
			}
			throw new ArgumentOutOfRangeException(nameof(length), "Message length exceeds the maximum supported size.");
		}
	}

	public static class PipeStreamExtensions
	{
		public static Task ReadExactlyAsync(this PipeStream pipe, byte[] buffer, CancellationToken cancellationToken = default)
			=> pipe.ReadExactlyAsync(buffer, 0, buffer.Length, cancellationToken);

		public static async Task ReadExactlyAsync(this PipeStream pipe, byte[] buffer, int offset, int count, CancellationToken cancellationToken = default)
		{
			if (pipe is null) throw new ArgumentNullException(nameof(pipe));
			if (buffer is null) throw new ArgumentNullException(nameof(buffer));
			if (offset < 0 || count < 0 || offset + count > buffer.Length) throw new ArgumentOutOfRangeException(nameof(count));

			int totalBytesRead = 0;
			while (totalBytesRead < count)
			{
				cancellationToken.ThrowIfCancellationRequested();
				int bytesRead = await pipe.ReadAsync(buffer, offset + totalBytesRead, count - totalBytesRead, cancellationToken).ConfigureAwait(false);
				if (bytesRead == 0) throw new EndOfStreamException();
				totalBytesRead += bytesRead;
			}
		}
	}

	public class ConnectionStatus
	{
		/// <summary>
		/// Gets or sets the connection status number.
		/// -1: Disconnected
		/// 0: Connected but no active sessions
		/// 1: Connected with active sessions
		/// </summary>
		[JsonPropertyName("status_num")]
		public int StatusNum { get; set; }

		/// <summary>
		/// Gets or sets whether the encryption key has been confirmed.
		/// </summary>
		[JsonPropertyName("key_confirmed")]
		public bool KeyConfirmed { get; set; }

		/// <summary>
		/// Gets or sets the last mouse movement timestamp.
		/// </summary>
		[JsonPropertyName("mouse_time")]
		public long MouseTime { get; set; }

		/// <summary>
		/// Gets or sets the client ID.
		/// </summary>
		[JsonPropertyName("id")]
		public string Id { get; set; } = string.Empty;

		/// <summary>
		/// Gets or sets the number of active video connections.
		/// </summary>
		[JsonPropertyName("video_conn_count")]
		public int VideoConnCount { get; set; }

		/// <summary>
		/// Gets a human-readable description of the connection status.
		/// </summary>
		[JsonIgnore]
		public string StatusDescription
		{
			get
			{
				return StatusNum switch
				{
					-1 => "-1: Disconnected",
					0 => "0: Connected (Idle)",
					1 => "1: Connected (Active)",
					_ => $"Unknown Status ({StatusNum})"
				};
			}
		}

		/// <summary>
		/// Gets whether the client is currently connected.
		/// </summary>
		[JsonIgnore]
		public bool IsConnected => StatusNum >= 0;

		/// <summary>
		/// Gets whether there are active sessions.
		/// </summary>
		[JsonIgnore]
		public bool HasActiveSessions => StatusNum > 0;
	}

}
