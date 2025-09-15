using AgentServer;
using Microsoft.AspNetCore.Authentication.JwtBearer;
using Microsoft.IdentityModel.Tokens;
using Microsoft.JSInterop.Infrastructure;
using System.Text;
using System.Text.Json;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddRazorPages();
builder.Services.AddSignalR(o =>
{
	o.MaximumReceiveMessageSize = 10 * 1024 * 1024;   // 10M
	o.EnableDetailedErrors = true;
}).AddMessagePackProtocol();

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
	   .AddJwtBearer(JwtBearerDefaults.AuthenticationScheme, opt =>
	   {
		   // 保留JWT配置用于API访问
		   var key = Encoding.UTF8.GetBytes(builder.Configuration["Jwt:SecretKey"]);
		   opt.TokenValidationParameters = new TokenValidationParameters
		   {
			   ValidateIssuer = false,
			   ValidateAudience = false,
			   ValidateLifetime = true,
			   ValidateIssuerSigningKey = true,
			   IssuerSigningKey = new SymmetricSecurityKey(key)
		   };
	   });

// 2. 需要将授权服务添加进来，否则 RequireAuthorization 会抛异常
builder.Services.AddAuthorization(options =>
{
	// 为API创建一个需要JWT认证的策略
	options.AddPolicy("ApiPolicy", policy =>
	{
		policy.AuthenticationSchemes.Add(JwtBearerDefaults.AuthenticationScheme);
		policy.RequireAuthenticatedUser();
	});
});

builder.Services.AddSingleton<AgentService>();
builder.Services.AddSingleton<JwtService>();

var app = builder.Build();

if (!app.Environment.IsDevelopment())
{
	app.UseExceptionHandler("/Error");
	app.UseHsts();
}

app.UseHttpsRedirection();
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
	=> await service.ExecutePowershellScript(dto.AgentId, dto.Script)).RequireAuthorization("ApiPolicy");
app.MapPost("/api/agent/screen", async (AgentService service, ScreenDTO dto) 
	=> Results.File(await service.CaptureScreen(dto.AgentId), "image/jpeg", "screen.jpg")).RequireAuthorization("ApiPolicy");
app.MapPost("/api/agent/remotedesk", async (AgentService service, ExecuteDTO dto)
	=> await service.RemoteDesk(dto.AgentId, builder.Configuration["RemoteDesk:Server"], builder.Configuration["RemoteDesk:Key"])).RequireAuthorization("ApiPolicy");

app.Run();