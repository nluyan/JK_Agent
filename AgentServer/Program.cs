using AgentServer;

var builder = WebApplication.CreateBuilder(args);

// 添加服务到容器中
builder.Services.AddRazorPages();
builder.Services.AddSignalR(); // 添加 SignalR 服务

var app = builder.Build();

if (!app.Environment.IsDevelopment())
{
	app.UseExceptionHandler("/Error");
	app.UseHsts();
}

app.UseHttpsRedirection();
app.UseStaticFiles();

app.UseRouting();

app.UseAuthorization();

app.MapRazorPages();
app.MapHub<AgentHub>("/AgentHub"); // 映射您的 Hub 到一个 URL

app.Run();