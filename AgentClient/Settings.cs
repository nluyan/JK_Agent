using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading.Tasks;

internal class Settings
{
	public const string Version = "2.20";

	// 启动错峰上限（秒）：进程启动后随机延迟再连接服务器，避免大批Agent同时冲击服务端
	// 可在 appsettings.json 中用 StartupJitter 覆盖，负数表示关闭错峰
	public const int DefaultStartupJitterSeconds = 120;

	// 升级错峰上限（秒）：发现新版本后随机延迟再下载，避免同时拉取升级包打满服务端带宽
	// 可在 appsettings.json 中用 UpdateJitter 覆盖，负数表示关闭错峰
	public const int DefaultUpdateJitterSeconds = 1800;
}
