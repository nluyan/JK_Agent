using AgentServer;
using Microsoft.AspNetCore.Authentication.JwtBearer;
using Microsoft.Extensions.Options;
using Microsoft.IdentityModel.Logging;
using Microsoft.IdentityModel.Tokens;
using Microsoft.JSInterop.Infrastructure;
using System.Diagnostics;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;


if (args.Contains("--install"))
{
	Install();
	return;
}
if (args.Contains("--uninstall"))
{
	Uninstall();
	return;
}

var builder = WebApplication.CreateBuilder(args);

// HTTPS 证书只解析一次并缓存：PKCS#12 解密开销很大（实测每次 TLS 握手约消耗 72ms CPU），
// 上千台 Agent 同时重连时会打满 CPU 并触发握手超时雪崩。
var httpsCertificate = new Lazy<X509Certificate2>(() =>
	X509CertificateLoader.LoadPkcs12(
		Convert.FromBase64String(builder.Configuration["HttpsCert:Data"] ?? string.Empty),
		builder.Configuration["HttpsCert:Password"]));

builder.WebHost.ConfigureKestrel(serverOptions =>
{
	serverOptions.ListenAnyIP(int.Parse(builder.Configuration["Port:Http"]));
	serverOptions.ListenAnyIP(int.Parse(builder.Configuration["Port:Https"]), listenOptions => listenOptions.UseHttps(c =>
	{
		c.ServerCertificateSelector = (connectionContext, host) => httpsCertificate.Value;
	}));
	serverOptions.Limits.MaxRequestBodySize = 100 * 1024 * 1024;
	serverOptions.AddServerHeader = false;   // 不返回 Server: Kestrel 响应头，避免指纹泄露
});

builder.Services.AddRazorPages();
builder.Services.AddSignalR(o =>
{
	o.MaximumReceiveMessageSize = 10 * 1024 * 1024;   // 10M
	o.EnableDetailedErrors = true;
}).AddMessagePackProtocol();

var isDevelopment = builder.Environment.IsDevelopment();
// ShowPII 会把令牌里的身份信息写进异常与诊断日志，仅开发环境开启
IdentityModelEventSource.ShowPII = isDevelopment;

var issuer = builder.Configuration["Jwt:Issuer"];
var authority = builder.Configuration["Jwt:Authority"];
var audience = builder.Configuration["Jwt:Audience"];
var secretKey = builder.Configuration["Jwt:SecretKey"];

// 1. 注册 JwtBearer 和 Cookie 认证
builder.Services
	   .AddAuthentication(options =>
	   {
		   options.DefaultAuthenticateScheme = "Cookies";
		   options.DefaultChallengeScheme = "Cookies";
	   })
	   .AddCookie("Cookies", options =>
	   {
		   options.LoginPath = "/Login";
		   options.AccessDeniedPath = "/Login";
		   options.Cookie.Name = "AgentAuthCookie";
		   options.ExpireTimeSpan = TimeSpan.FromDays(30);
		   options.SlidingExpiration = true;
	   })
	   //.AddJwtBearer(JwtBearerDefaults.AuthenticationScheme, opt =>
	   //{
	   // // 保留JWT配置用于API访问
	   // var key = Encoding.UTF8.GetBytes(builder.Configuration["Jwt:SecretKey"]);
	   // opt.TokenValidationParameters = new TokenValidationParameters
	   // {
	   //  ValidateIssuer = false,
	   //  ValidateAudience = false,
	   //  ValidateLifetime = true,
	   //  ValidateIssuerSigningKey = true,
	   //  IssuerSigningKey = new SymmetricSecurityKey(key)
	   // };
	   //})
	   .AddJwtBearer("ApplicationAuthority", options =>
	   {
		   options.Authority = authority;

		   options.TokenValidationParameters = new TokenValidationParameters
		   {
			   NameClaimType = "name",
			   ValidateIssuer = false,
			   //ValidIssuer = auth.Authority,
			   ValidateAudience = true,
			   ValidAudience = audience,
			   ValidateLifetime = true,
		   };

		   // 诊断事件：运行时在控制台/日志输出详细原因
		   options.Events = new JwtBearerEvents
		   {
			   OnMessageReceived = ctx =>
			   {
				   if (isDevelopment) Console.WriteLine($"收到验证请求: {ctx.Request.Path}");   // 不输出Authorization头：Bearer令牌落盘等于泄露凭据
				   return Task.CompletedTask;
			   },
			   OnTokenValidated = ctx =>
			   {

				   if (isDevelopment) Console.WriteLine("验证通过:" + ctx.Principal?.Identity?.Name);
				   return Task.CompletedTask;
			   },
			   OnAuthenticationFailed = ctx =>
			   {
				   Console.WriteLine($"验证失败: {ctx.Exception?.GetType().Name} \n {ctx.Exception?.Message} \n {ctx.Exception.InnerException?.Message}");
				   return Task.CompletedTask;
			   }
		   };
	   });


// 2. 需要将授权服务添加进来，否则 RequireAuthorization 会抛异常
builder.Services.AddAuthorization(options =>
{
	// 为API创建一个需要JWT认证的策略
	options.AddPolicy("ApiPolicy", policy =>
	{
		policy.AuthenticationSchemes.Add("ApplicationAuthority");
		policy.RequireAuthenticatedUser();
	});
});

// CMDB上报改用IHttpClientFactory复用连接：上千台Agent注册/重连时避免每次new HttpClient耗尽端口
builder.Services.AddHttpClient(AgentService.CmdbHttpClientName, client =>
{
	client.Timeout = TimeSpan.FromSeconds(15);
});

builder.Services.AddSingleton<AgentService>();
builder.Services.AddSingleton<JwtService>();

builder.Services.AddCors(cors => cors
		.AddDefaultPolicy(policy => policy
		.AllowAnyOrigin()
		.AllowAnyHeader()
		.AllowAnyMethod()
		.WithExposedHeaders("*")));

var app = builder.Build();

// 部署前提醒：下面两项处于关闭状态时，任何能访问端口的人都能对Agent下发命令
if (string.IsNullOrEmpty(app.Configuration["HubAuthToken"]))
	app.Logger.LogWarning("未配置 HubAuthToken，/AgentHub 处于无鉴权状态：任何能访问该端口的人都可以连接Hub并向Agent下发命令");
if (app.Configuration["ApiAuth"] == "false")
	app.Logger.LogWarning("ApiAuth=false，/api/agent/* 允许匿名调用（含远程执行PowerShell），生产环境建议开启");

app.UseCors();
if (!app.Environment.IsDevelopment())
{
	app.UseExceptionHandler("/Error");
	app.UseHsts();
}

//app.UseHttpsRedirection();
app.UseStaticFiles();

app.UseRouting();

// Hub鉴权放在HTTP层：令牌不对直接返回401，而不是先接受WebSocket连接再Abort。
// 后者会让客户端认为“连接成功过”，SignalR的重连计数被清零，从而陷入每秒数次的高速重连循环；
// 2200台Agent令牌配错时足以把服务端打满。
app.Use(async (context, next) =>
{
	if (context.Request.Path.StartsWithSegments("/AgentHub") && !HubAuth.IsValid(app.Configuration, context))
	{
		context.Response.StatusCode = StatusCodes.Status401Unauthorized;
		return;
	}
	await next(context);
});
app.UseAuthentication();
app.UseAuthorization();

app.MapRazorPages();
app.MapHub<AgentHub>("/AgentHub");


app.MapPost("/api/login", (JwtService jwt, IConfiguration config, LoginDto dto) =>
{
	var adminUsername = config["Admin:Username"];
	var adminPassword = config["Admin:Password"];

	if (dto.Username == adminUsername && dto.Password == adminPassword)
	{
		var token = jwt.CreateToken(DateTime.Now + TimeSpan.FromDays(30));
		return Results.Ok(new { token, success = true });
	}
	else
		return Results.BadRequest(new { success = false, message = "用户名或密码错误" });
});

// ApiAuth 只有显式等于 "false" 时才放开匿名访问；缺失或写成其它值一律按开启处理，
// 避免线上 appsettings.json 漏配时静默退化成“任何人都能远程执行 PowerShell”。
var apiAuthDisabled = app.Configuration["ApiAuth"] == "false";

// 四个 /api/agent/* 端点只注册一次：原先 if/else 两个分支各写一份，
// 以后新增端点或改签名极易只改一边，造成鉴权分支与匿名分支行为不一致。
var agentApi = app.MapGroup("/api/agent");
if (apiAuthDisabled)
	agentApi.AllowAnonymous();
else
	agentApi.RequireAuthorization("ApiPolicy");

agentApi.MapGet("/list", (AgentService service) => service.GetAgents());
agentApi.MapPost("/execute", async (AgentService service, HttpRequest request, ExecuteRequestDTO dto)
	=> await service.ExecutePowershellScript(
		dto.ToExecuteDTO(),
		dto.GetScriptText(),
		request.Headers["X-JK-Agent-Command"].FirstOrDefault()));
agentApi.MapPost("/printer-driver/install", async (AgentService service, PrinterDriverInstallDTO dto)
	=> await service.InstallPrinterDriver(dto));
agentApi.MapPost("/remotedesk", async (AgentService service, RemoteDeskDTO dto)
	=> await service.RemoteDesk(dto));


	app.Run();


void Install()
{
	if (!OperatingSystem.IsLinux())
	{
		Console.Error.WriteLine("--install 仅支持 Linux（systemd）。");
		Environment.Exit(1);
		return;
	}

	var dir = Directory.GetCurrentDirectory();
	var binary = Path.Combine(dir, "AgentServer");
	if (!File.Exists(binary))
	{
		Console.Error.WriteLine($"没有找到 {binary}");
		Console.Error.WriteLine("请先把发布包整体解压到安装目录（例如 /opt/agentserver），cd 进去再执行 ./AgentServer --install");
		Environment.Exit(1);
		return;
	}

	// 发布包是在 Windows 上打的，zip 不一定带 Unix 权限位，解压出来可能没有可执行权限
	File.SetUnixFileMode(binary,
		UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute |
		UnixFileMode.GroupRead | UnixFileMode.GroupExecute |
		UnixFileMode.OtherRead | UnixFileMode.OtherExecute);

	// 本文件在 Windows 上是 CRLF，逐字字符串会把 \r 一起带进来。systemd 不会去掉行尾的 \r，
	// ExecStart 会变成一个尾部带 \r 的路径而根本起不来，所以这里强制转成 LF。
	var content = ($@"[Unit]
Description=AgentServer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory={dir}
ExecStart={binary}
Restart=always
RestartSec=5
TimeoutStopSec=30
LimitNOFILE=65536
Environment=ASPNETCORE_ENVIRONMENT=Production
StandardOutput=journal
StandardError=journal
SyslogIdentifier=AgentServer

[Install]
WantedBy=multi-user.target").Replace("\r\n", "\n");

	const string unitPath = "/etc/systemd/system/agentserver.service";
	try
	{
		// 每次执行都重写：安装目录可能换过，升级时也必须让 systemd 读到新配置。
		// 旧实现只在文件不存在时才写，结果升级时跑 --install 什么都不做，老进程还在跑。
		File.WriteAllText(unitPath, content);
	}
	catch (UnauthorizedAccessException)
	{
		Console.Error.WriteLine($"写 {unitPath} 被拒绝，请用 root 或 sudo 执行。");
		Environment.Exit(1);
		return;
	}
	Console.WriteLine($"已写入 {unitPath}");
	Console.WriteLine($"  WorkingDirectory={dir}");
	Console.WriteLine($"  ExecStart={binary}");

	if (RunSystemctl("daemon-reload") != 0)
	{
		Environment.Exit(1);
	}
	if (RunSystemctl("enable agentserver.service") != 0)
	{
		Environment.Exit(1);
	}
	// 用 restart 而不是 start：升级场景下服务本来就在跑，start 只会报 already active 然后什么也不做
	if (RunSystemctl("restart agentserver.service") != 0)
	{
		Environment.Exit(1);
	}

	Console.WriteLine();
	RunSystemctl("--no-pager status agentserver.service");
	Console.WriteLine();
	Console.WriteLine("服务已安装并启动。常用命令：");
	Console.WriteLine("  systemctl status agentserver");
	Console.WriteLine("  journalctl -u agentserver -f");
	Console.WriteLine("  ./AgentServer --uninstall");
	Console.WriteLine();
	Console.WriteLine("端口与证书都在同目录的 appsettings.json（Port:Http / Port:Https / HttpsCert），");
	Console.WriteLine("改完要 systemctl restart agentserver 才生效。");
}

void Uninstall()
{
	if (!OperatingSystem.IsLinux())
	{
		Console.Error.WriteLine("--uninstall 仅支持 Linux（systemd）。");
		Environment.Exit(1);
		return;
	}

	const string unitPath = "/etc/systemd/system/agentserver.service";
	RunSystemctl("stop agentserver.service");
	RunSystemctl("disable agentserver.service");
	if (File.Exists(unitPath))
	{
		try
		{
			File.Delete(unitPath);
		}
		catch (UnauthorizedAccessException)
		{
			Console.Error.WriteLine($"删除 {unitPath} 被拒绝，请用 root 或 sudo 执行。");
			Environment.Exit(1);
			return;
		}
		Console.WriteLine($"已删除 {unitPath}");
	}
	else
	{
		Console.WriteLine($"{unitPath} 不存在，跳过删除。");
	}
	RunSystemctl("daemon-reload");
	Console.WriteLine("AgentServer uninstalled.");
}

// systemctl 的输出直接透传到终端，这里只关心退出码
int RunSystemctl(string arguments)
{
	try
	{
		var process = Process.Start("systemctl", arguments);
		if (process == null)
		{
			Console.Error.WriteLine($"无法启动 systemctl {arguments}");
			return -1;
		}
		process.WaitForExit();
		if (process.ExitCode != 0)
		{
			Console.Error.WriteLine($"systemctl {arguments} 失败，退出码 {process.ExitCode}");
		}
		return process.ExitCode;
	}
	catch (Exception ex)
	{
		Console.Error.WriteLine($"执行 systemctl {arguments} 出错：{ex.Message}");
		Console.Error.WriteLine("请确认这台机器用的是 systemd，并且以 root 身份执行。");
		return -1;
	}
}
