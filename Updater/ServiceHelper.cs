using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Text;
using System.Threading.Tasks;

namespace Updater
{
	public static class ServiceHelper
	{

		public static void StartService(string serviceName)
		{
			Process.Start(new ProcessStartInfo 
			{
				FileName = "sc.exe",
				Arguments = $"start {serviceName}",
				UseShellExecute = false,
				CreateNoWindow = true,
			});
		}

		/// <summary>
		/// 停止服务，超时未停抛 TimeoutException
		/// </summary>
		public static void StopService(string serviceName, int timeoutSec = 30)
		{
			Process.Start(new ProcessStartInfo
			{
				FileName = "sc.exe",
				Arguments = $"stop {serviceName}",
				UseShellExecute = false,
				CreateNoWindow = true,
			});
		}
	}
}
