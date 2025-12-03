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

builder.WebHost.ConfigureKestrel(serverOptions =>
{
	serverOptions.ListenAnyIP(int.Parse(builder.Configuration["Port:Http"]));
	serverOptions.ListenAnyIP(int.Parse(builder.Configuration["Port:Https"]), listenOptions => listenOptions.UseHttps(c =>
	{
		c.ServerCertificateSelector = (connectionContext, host) =>
		{
			var cert = Convert.FromBase64String(builder.Configuration["HttpsCert:Data"]);
			var password = builder.Configuration["HttpsCert:Password"];
			return new X509Certificate2(cert, password);
		};
	}));
	serverOptions.Limits.MaxRequestBodySize = 100 * 1024 * 1024;
});

builder.Services.AddRazorPages();
builder.Services.AddSignalR(o =>
{
	o.MaximumReceiveMessageSize = 10 * 1024 * 1024;   // 10M
	o.EnableDetailedErrors = true;
}).AddMessagePackProtocol();

IdentityModelEventSource.ShowPII = true;

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
				   Console.WriteLine($"收到验证请求: {ctx.Request.Path} \ntoken: {ctx.Request.Headers.Authorization}");
				   return Task.CompletedTask;
			   },
			   OnTokenValidated = ctx =>
			   {

				   Console.WriteLine("验证通过:" + ctx.Principal.Identity.Name);
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

builder.Services.AddSingleton<AgentService>();
builder.Services.AddSingleton<JwtService>();

builder.Services.AddCors(cors => cors
		.AddDefaultPolicy(policy => policy
		.AllowAnyOrigin()
		.AllowAnyHeader()
		.AllowAnyMethod()
		.WithExposedHeaders("*")));

var app = builder.Build();

app.UseCors();
if (!app.Environment.IsDevelopment())
{
	app.UseExceptionHandler("/Error");
	app.UseHsts();
}

//app.UseHttpsRedirection();
app.UseStaticFiles();

app.UseRouting();

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

app.MapGet("/api/agent/list", (AgentService service)
	=> service.GetAgents()).RequireAuthorization("ApiPolicy");
app.MapPost("/api/agent/execute", async (AgentService service, ExecuteDTO dto)
	=> await service.ExecutePowershellScript(dto, dto.Script)).RequireAuthorization("ApiPolicy");
app.MapPost("/api/agent/remotedesk", async (AgentService service, RemoteDeskDTO dto)
	=> await service.RemoteDesk(dto)).RequireAuthorization("ApiPolicy");


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