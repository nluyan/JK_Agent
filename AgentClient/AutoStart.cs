using System;
using System.Diagnostics;
using System.IO;
using Microsoft.Win32;

namespace AgentClient
{
	public static class AutoStart
	{
		const string RegKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
		const string AppName = "JK_Agent_Client";   // 注册表项名称，保持唯一即可

		/// <summary>
		/// 设置或取消开机自启
		/// </summary>
		/// <param name="enable">true 开启，false 关闭</param>
		public static void SetAutoStart()
		{
			// 取当前可执行文件完整路径
			string exePath = Process.GetCurrentProcess().MainModule.FileName;

			using (RegistryKey runKey = Registry.CurrentUser.OpenSubKey(RegKey, true))
			{
				if (runKey == null) throw new Exception("无法打开注册表键");
				// 如果路径里含空格，加一对引号
				string path = exePath.Contains(" ") ? $"\"{exePath}\"" : exePath;
				runKey.SetValue(AppName, path);
			}
		}
	}
}
