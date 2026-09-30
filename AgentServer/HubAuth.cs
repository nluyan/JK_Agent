using System.Security.Cryptography;
using System.Text;

namespace AgentServer
{
	/// <summary>
	/// /AgentHub 的令牌校验。令牌取自配置项 HubAuthToken，留空表示不启用鉴权（兼容尚未下发令牌的旧客户端）。
	/// WebSocket 无法自定义请求头，SignalR 约定用 access_token 查询串传递；
	/// .NET 客户端在 negotiate 等 HTTP 请求上则会放进 Authorization 头，两种都要认。
	/// </summary>
	public static class HubAuth
	{
		private const string TokenConfigKey = "HubAuthToken";
		private const string AccessTokenQueryKey = "access_token";
		private const string BearerPrefix = "Bearer ";

		// 是否启用了Hub鉴权；未启用时任何连接都放行，保持与旧客户端兼容
		public static bool IsEnabled(IConfiguration configuration) =>
			!string.IsNullOrEmpty(configuration[TokenConfigKey]);

		public static bool IsValid(IConfiguration configuration, HttpContext? httpContext)
		{
			var expectedToken = configuration[TokenConfigKey];
			if (string.IsNullOrEmpty(expectedToken))
				return true;

			var providedToken = GetProvidedToken(httpContext);
			if (string.IsNullOrEmpty(providedToken))
				return false;

			// 定长时间比较，避免通过响应耗时逐字节猜出令牌
			var providedBytes = Encoding.UTF8.GetBytes(providedToken);
			var expectedBytes = Encoding.UTF8.GetBytes(expectedToken);
			return providedBytes.Length == expectedBytes.Length
				&& CryptographicOperations.FixedTimeEquals(providedBytes, expectedBytes);
		}

		private static string? GetProvidedToken(HttpContext? httpContext)
		{
			if (httpContext == null)
				return null;

			var fromQuery = httpContext.Request.Query[AccessTokenQueryKey].ToString();
			if (!string.IsNullOrEmpty(fromQuery))
				return fromQuery;

			var authorization = httpContext.Request.Headers.Authorization.ToString();
			return authorization.StartsWith(BearerPrefix, StringComparison.OrdinalIgnoreCase)
				? authorization[BearerPrefix.Length..]
				: null;
		}
	}
}
