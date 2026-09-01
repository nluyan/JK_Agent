using System.Text.Json;

namespace AgentServer
{
	public class DTOBase
	{
		public string AgentId { get; set; }

		public string Serial { get; set; }

		public string Ip { get; set; }
	}

	public class ExecuteDTO : DTOBase
	{
		public bool Native { get; set; }
		public string Script { get; set; }
	}

	// HTTP 接口请求模型：Script 支持字符串或原始 JSON 对象。
	// 当使用 X-JK-Agent-Command: SNMP 时，可以直接提交 JSON 对象，避免在字符串中嵌套转义 JSON。
	public class ExecuteRequestDTO : DTOBase
	{
		public bool Native { get; set; }
		public JsonElement Script { get; set; }

		public ExecuteDTO ToExecuteDTO() => new()
		{
			AgentId = AgentId,
			Serial = Serial,
			Ip = Ip,
			Native = Native
		};

		public string GetScriptText() => Script.ValueKind switch
		{
			JsonValueKind.String => Script.GetString() ?? string.Empty,
			JsonValueKind.Undefined or JsonValueKind.Null => string.Empty,
			_ => Script.GetRawText()
		};
	}

	public class ScreenDTO : DTOBase { }

	public class RemoteDeskDTO : DTOBase { }

	public class PrinterDriverInstallDTO : DTOBase
	{
		public string DriverUrl { get; set; }
	}

	public class LoginDto
	{
		public string Username { get; set; }

		public string Password { get; set; }
	}

	public class ExecuteResult
	{
		public int Status { get; set; }

		public string? Result { get; set; }
	}
}
