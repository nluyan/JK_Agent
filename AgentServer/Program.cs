using AgentServer;

var builder = WebApplication.CreateBuilder(args);

// ��ӷ���������
builder.Services.AddRazorPages();
builder.Services.AddSignalR(); // ��� SignalR ����

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
app.MapHub<AgentHub>("/AgentHub"); // ӳ������ Hub ��һ�� URL

app.Run();