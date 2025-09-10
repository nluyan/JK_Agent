using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading.Tasks;

namespace Updater
{
	internal class Program
	{
		static void Main(string[] args)
		{
			try
			{
				foreach (var process in Process.GetProcessesByName("AgentClient"))
				{
					try
					{
						var ss = process.MainModule.FileName;
						process.Kill();
					}
					catch { }
				}

				if (Directory.Exists("temp"))
				{
					CopyDirectory(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "temp"), AppDomain.CurrentDomain.BaseDirectory);
					Directory.Delete("temp", true);
				}

				if (File.Exists("AgentClient.zip"))
					File.Delete("AgentClient.zip");

				// 启动新版本
				Process.Start(new ProcessStartInfo
				{
					FileName = "AgentClient.exe",
					Arguments = "run",
					UseShellExecute = true,
					CreateNoWindow = true,
					WorkingDirectory = AppDomain.CurrentDomain.BaseDirectory,
					WindowStyle = ProcessWindowStyle.Hidden
				});
			}
			catch(Exception ex)
			{
				File.WriteAllText("update_log.txt", ex.Message);
			}
		}

		static void CopyDirectory(string src, string dst, bool overwrite = true)
		{
			if (src == null) throw new ArgumentNullException(nameof(src));
			if (dst == null) throw new ArgumentNullException(nameof(dst));

			// 1.  normalize 路径，支持 "..\xxx" 这种相对路径
			var srcDir = new DirectoryInfo(Path.GetFullPath(src));
			var dstDir = new DirectoryInfo(Path.GetFullPath(dst));

			if (!srcDir.Exists)
				throw new DirectoryNotFoundException($"源目录不存在: {srcDir.FullName}");

			// 2.  递归拷贝
			CopyRecursive(srcDir, dstDir, overwrite);
		}

		static void CopyRecursive(DirectoryInfo src, DirectoryInfo dst, bool overwrite)
		{
			// 保证目标目录存在
			if (!dst.Exists)
				dst.Create();

			// 先拷文件
			foreach (var file in src.GetFiles())
			{
				var dstFile = new FileInfo(Path.Combine(dst.FullName, file.Name));
				try
				{
					file.CopyTo(dstFile.FullName, overwrite);
				}
				catch { }
			}

			// 再递归子目录
			foreach (var subDir in src.GetDirectories())
			{
				var dstSub = new DirectoryInfo(Path.Combine(dst.FullName, subDir.Name));
				CopyRecursive(subDir, dstSub, overwrite);
			}
		}
	}
}
