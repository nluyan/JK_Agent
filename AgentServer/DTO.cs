using System.Text.Json;
using System.Text.Json.Serialization;

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

	// Agent 原生命令（水印/壁纸）统一使用 camelCase，并省略未赋值的可选字段，
	// 这样终端能区分"没有下发这个字段"和"下发成默认值"。
	internal static class AgentCommandJson
	{
		public static readonly JsonSerializerOptions Options = new()
		{
			PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
			DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
		};
	}

	// 屏幕水印命令。服务端不持久化任何水印配置，文案下发后由终端自行保存，
	// 服务重启后终端按本地策略自动恢复水印。
	public class WatermarkDTO : DTOBase
	{
		// show=启用 update=改样式或文案 hide=停用并清除 query=回读终端状态
		public string? Operation { get; set; }

		// 支持占位符：{HostName}/{Serial}/{IP}/{Group}/{AgentId} 由服务端替换，
		// {UserName}/{SessionId}/{DateTime} 由终端在每次投递时替换。
		public string? Text { get; set; }

		public int? Opacity { get; set; }

		// 十六进制颜色，例如 #FFFFFF
		public string? Color { get; set; }

		public int? FontSize { get; set; }

		// 倾斜角度，-90 ~ 90
		public int? Angle { get; set; }

		public int? SpacingX { get; set; }

		public int? SpacingY { get; set; }

		public ExecuteDTO ToExecuteDTO() => new()
		{
			AgentId = AgentId,
			Serial = Serial,
			Ip = Ip
		};

		// 未提供的字段直接省略，终端据此保留本地已有的样式设置。
		public string ToCommandJson() => JsonSerializer.Serialize(new
		{
			Operation = string.IsNullOrWhiteSpace(Operation) ? "show" : Operation.Trim().ToLowerInvariant(),
			Text = string.IsNullOrWhiteSpace(Text) ? null : Text,
			Opacity,
			Color = string.IsNullOrWhiteSpace(Color) ? null : Color.Trim(),
			FontSize,
			Angle,
			SpacingX,
			SpacingY
		}, AgentCommandJson.Options);
	}

	// 壁纸命令。图片可 base64 内嵌，也可给 URL 由终端下载。
	public class WallpaperDTO : DTOBase
	{
		// set=设置 clear=恢复默认并解除锁定 query=回读当前生效配置
		public string? Operation { get; set; }

		public string? ImageBase64 { get; set; }

		public string? ImageUrl { get; set; }

		public string? FileName { get; set; }

		// 对应 WallpaperStyle：0=居中 2=拉伸 6=适应 10=填充 22=跨区
		public int? Style { get; set; }

		public bool? Tile { get; set; }

		// true 时写入策略键，"设置→个性化"里的壁纸选项变灰，用户改不掉。
		public bool? Lock { get; set; }

		public ExecuteDTO ToExecuteDTO() => new()
		{
			AgentId = AgentId,
			Serial = Serial,
			Ip = Ip
		};

		public string ToCommandJson() => JsonSerializer.Serialize(new
		{
			Operation = string.IsNullOrWhiteSpace(Operation) ? "set" : Operation.Trim().ToLowerInvariant(),
			ImageBase64 = string.IsNullOrWhiteSpace(ImageBase64) ? null : ImageBase64,
			ImageUrl = string.IsNullOrWhiteSpace(ImageUrl) ? null : ImageUrl.Trim(),
			FileName = string.IsNullOrWhiteSpace(FileName) ? null : FileName.Trim(),
			Style,
			Tile,
			Lock
		}, AgentCommandJson.Options);
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
