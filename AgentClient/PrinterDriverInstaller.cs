using System.Diagnostics;
using System.IO.Compression;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using Serilog;

namespace AgentClient;

internal static class PrinterDriverInstaller
{
	private static readonly HttpClient HttpClient = new()
	{
		Timeout = TimeSpan.FromMinutes(10)
	};

	public static async Task<PrinterDriverInstallResult> InstallAsync(string driverUrl, CancellationToken stoppingToken)
	{
		if (!RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
			return Failure("打印机驱动安装功能仅支持 Windows");

		if (!Uri.TryCreate(driverUrl, UriKind.Absolute, out var uri)
			|| (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
			return Failure("驱动包地址必须是有效的 HTTP 或 HTTPS 地址");

		var workDirectory = Path.Combine(
			Path.GetTempPath(),
			"JikeAgent",
			"PrinterDriver",
			Guid.NewGuid().ToString("N"));
		var archivePath = Path.Combine(workDirectory, "driver-package.zip");
		var extractDirectory = Path.Combine(workDirectory, "package");

		Directory.CreateDirectory(workDirectory);

		using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(stoppingToken);
		timeoutCts.CancelAfter(TimeSpan.FromMinutes(10));
		var cancellationToken = timeoutCts.Token;

		try
		{
			Log.Information("开始下载打印机驱动包: {DriverUrl}", driverUrl);

			using var response = await HttpClient.GetAsync(
				uri,
				HttpCompletionOption.ResponseHeadersRead,
				cancellationToken);
			response.EnsureSuccessStatusCode();

			await using (var responseStream = await response.Content.ReadAsStreamAsync(cancellationToken))
			await using (var outputStream = new FileStream(
				archivePath,
				FileMode.Create,
				FileAccess.Write,
				FileShare.None,
				bufferSize: 81920,
				useAsync: true))
			{
				await responseStream.CopyToAsync(outputStream, cancellationToken);
			}

			Directory.CreateDirectory(extractDirectory);
			try
			{
				ZipFile.ExtractToDirectory(archivePath, extractDirectory);
			}
			catch (Exception ex) when (ex is InvalidDataException || ex is IOException || ex is UnauthorizedAccessException)
			{
				return Failure($"驱动包不是有效的 ZIP 压缩包: {ex.Message}");
			}

			var infFiles = Directory.EnumerateFiles(
				extractDirectory,
				"*.inf",
				SearchOption.AllDirectories)
				.ToList();

			if (infFiles.Count == 0)
				return Failure("驱动包中未找到 INF 文件");

			if (infFiles.Count > 1)
				return Failure($"驱动包中找到多个 INF 文件，无法自动确定入口文件: {string.Join(", ", infFiles.Select(Path.GetFileName))}");

			var infPath = infFiles[0];
			Log.Information("找到打印机驱动 INF: {InfPath}", infPath);

			var installResult = await RunPnPUtilAsync(infPath, cancellationToken);
			return installResult with
			{
				InfPath = Path.GetRelativePath(extractDirectory, infPath)
			};
		}
		catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
		{
			return Failure(stoppingToken.IsCancellationRequested
				? "打印机驱动安装已取消"
				: "打印机驱动安装超时");
		}
		catch (HttpRequestException ex)
		{
			return Failure($"下载驱动包失败: {ex.Message}");
		}
		catch (Exception ex)
		{
			Log.Error(ex, "安装打印机驱动失败");
			return Failure($"安装打印机驱动失败: {ex.Message}");
		}
		finally
		{
			try
			{
				if (Directory.Exists(workDirectory))
					Directory.Delete(workDirectory, recursive: true);
			}
			catch (Exception ex)
			{
				Log.Warning(ex, "清理打印机驱动临时目录失败: {WorkDirectory}", workDirectory);
			}
		}
	}

	private static async Task<PrinterDriverInstallResult> RunPnPUtilAsync(
		string infPath,
		CancellationToken cancellationToken)
	{
		var startInfo = new ProcessStartInfo
		{
			FileName = "pnputil.exe",
			UseShellExecute = false,
			CreateNoWindow = true,
			RedirectStandardOutput = true,
			RedirectStandardError = true,
			StandardOutputEncoding = Encoding.UTF8,
			StandardErrorEncoding = Encoding.UTF8
		};
		startInfo.ArgumentList.Add("/add-driver");
		startInfo.ArgumentList.Add(infPath);
		startInfo.ArgumentList.Add("/install");

		using var process = new Process { StartInfo = startInfo };
		if (!process.Start())
			return Failure("无法启动 pnputil.exe");

		var outputTask = process.StandardOutput.ReadToEndAsync(cancellationToken);
		var errorTask = process.StandardError.ReadToEndAsync(cancellationToken);

		try
		{
			await process.WaitForExitAsync(cancellationToken);
		}
		catch (OperationCanceledException)
		{
			try
			{
				if (!process.HasExited)
					process.Kill(entireProcessTree: true);
			}
			catch (Exception ex)
			{
				Log.Debug(ex, "终止 pnputil.exe 失败");
			}
			throw;
		}

		var output = await outputTask;
		var error = await errorTask;
		var combinedOutput = string.Join(
			Environment.NewLine,
			new[] { output, error }.Where(text => !string.IsNullOrWhiteSpace(text)));
		var success = process.ExitCode == 0;
		var rebootRequired = combinedOutput.Contains("restart", StringComparison.OrdinalIgnoreCase)
			|| combinedOutput.Contains("reboot", StringComparison.OrdinalIgnoreCase)
			|| combinedOutput.Contains("重启", StringComparison.OrdinalIgnoreCase)
			|| process.ExitCode == 3010;

		return new PrinterDriverInstallResult
		{
			Success = success,
			Message = success ? "打印机驱动安装成功" : "打印机驱动安装失败",
			ExitCode = process.ExitCode,
			Output = combinedOutput,
			RebootRequired = rebootRequired
		};
	}

	private static PrinterDriverInstallResult Failure(string message) => new()
	{
		Success = false,
		Message = message,
		Output = message
	};
}

internal sealed record PrinterDriverInstallResult
{
	public bool Success { get; init; }
	public string Message { get; init; } = string.Empty;
	public int ExitCode { get; init; }
	public string Output { get; init; } = string.Empty;
	public bool RebootRequired { get; init; }
	public string? InfPath { get; init; }

	public string ToJson() => JsonSerializer.Serialize(this);
}
