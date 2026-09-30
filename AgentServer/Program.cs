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
	if (!File.Exists(Path.Combine(Directory.GetCurrentDirectory(), "AgentServer")))
	{
		Console.WriteLine("没有找到AgentServer");
		Environment.Exit(0);
	}

	var file = "/etc/systemd/system/agentserver.service";
	if (!File.Exists(file))
	{
		var content = $@"[Unit]
Description=AgentServer
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory={Directory.GetCurrentDirectory()}
ExecStart={Directory.GetCurrentDirectory()}/AgentServer
Restart=always
RestartSec=5
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal
SyslogIdentifier=AgentServer

[Install]
WantedBy=multi-user.target";
		File.WriteAllText(file, content);

		Process.Start("systemctl", "enable agentserver.service").WaitForExit();
		Process.Start("systemctl", "start agentserver.service").WaitForExit();

		Console.WriteLine("AgentServer installed.");
	}
}

void Uninstall()
{
	Process.Start("systemctl", "stop agentserver.service").WaitForExit();
	Process.Start("systemctl", "disable agentserver.service").WaitForExit();
	File.Delete("/etc/systemd/system/agentserver.service");
	Console.WriteLine("AgentServer uninstalled.");
}
