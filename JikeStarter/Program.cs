using Microsoft.Win32;
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Security.Principal;
using System.Text;
using System.Threading.Tasks;

namespace JikeStarter
{
	internal class Program
	{
		static void Main(string[] args)
		{
			if(args.Length == 0)
			{
				Register();
			}
			else
			{
				Process.Start("rd.exe");
			}
		}

		static void Register()
		{
			// 要求管理员权限
			if (!IsRunAsAdmin())
			{
				Console.WriteLine("请以管理员身份重新运行！");
				Console.ReadKey();
				return;
			}

			// 根路径
			const string root = @"jike";

			// 1. 创建主键并写入默认值和 URL Protocol
			using (var jike = Registry.ClassesRoot.CreateSubKey(root))
			{
				jike.SetValue("", "Jike Remote Desktop Protocol");   // 默认字符串
				jike.SetValue("URL Protocol", "", RegistryValueKind.String); // 空字符串
			}

			string exePath = System.Diagnostics.Process.GetCurrentProcess().MainModule.FileName;

			// 2. DefaultIcon
			using (var icon = Registry.ClassesRoot.CreateSubKey(@"jike\DefaultIcon"))
			{
				icon.SetValue("", $"\"{exePath}\",0");
			}

			// 3. shell 和 open 只是容器，不需要写值
			Registry.ClassesRoot.CreateSubKey(@"jike\shell");
			Registry.ClassesRoot.CreateSubKey(@"jike\shell\open");

			// 4. command
			using (var cmd = Registry.ClassesRoot.CreateSubKey(@"jike\shell\open\command"))
			{
				cmd.SetValue("", $"\"{exePath}\" \"%1\"");
			}

			Console.WriteLine("系统已经设置完成！按任意键退出");
			Console.ReadKey();
		}

		static bool IsRunAsAdmin()
		{
			var id = WindowsIdentity.GetCurrent();
			var principal = new WindowsPrincipal(id);
			return principal.IsInRole(WindowsBuiltInRole.Administrator);
		}
	}
}
