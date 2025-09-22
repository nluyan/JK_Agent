using Serilog;
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Threading;

namespace AgentClient
{
    internal static class ScreenCapture
    {
        public static void StartCapture()
        {
            uint sessionId = WTSGetActiveConsoleSessionId();
            if (sessionId == 0xFFFFFFFF)
            {
                Log.Error("StartCapture: No active console session found.");
                throw new InvalidOperationException("当前没有用户");
            }
            
            IntPtr hImpersonationToken = IntPtr.Zero;
            if (!WTSQueryUserToken((int)sessionId, out hImpersonationToken))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error(), "WTSQueryUserToken 失败");
            }

            IntPtr hPrimaryToken = IntPtr.Zero;
            IntPtr lpEnvironment = IntPtr.Zero;
            PROCESS_INFORMATION pi = new PROCESS_INFORMATION();
            try
            {
                var sa = new SECURITY_ATTRIBUTES();
                sa.nLength = Marshal.SizeOf(sa);

                if (!DuplicateTokenEx(
                    hImpersonationToken,
                    (uint)TOKEN_ACCESS_LEVEL.MAXIMUM_ALLOWED,
                    ref sa,
                    (int)SECURITY_IMPERSONATION_LEVEL.SecurityIdentification,
                    (int)TOKEN_TYPE.TokenPrimary,
                    out hPrimaryToken))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error(), "DuplicateTokenEx 失败");
                }

                if (!CreateEnvironmentBlock(out lpEnvironment, hPrimaryToken, false))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateEnvironmentBlock 失败");
                }

                var si = new STARTUPINFO();
                si.cb = Marshal.SizeOf(si);
                si.lpDesktop = "winsta0\\default";
                si.dwFlags = 0x00000001; // STARTF_USESHOWWINDOW
                si.wShowWindow = 1;     // SW_SHOWNORMAL

                string exeDir = AppDomain.CurrentDomain.BaseDirectory;
                string helperPath = Path.Combine(exeDir, "ScreenCapture.exe");

                if (!File.Exists(helperPath))
                {
                    Log.Error("截图助手程序不存在: " + helperPath);
                    throw new FileNotFoundException("找不到 ScreenCapture.exe", helperPath);
                }

                Log.Debug("准备在用户会话 {sessionId} 中执行屏幕截图程序: {path}", sessionId, helperPath);

                int dwCreationFlags = 0 | 0x00000400; // CREATE_UNICODE_ENVIRONMENT

                bool ok = CreateProcessAsUser(
                    hPrimaryToken,
                    null,
                    helperPath,
                    IntPtr.Zero,
                    IntPtr.Zero,
                    false,
                    dwCreationFlags,
                    lpEnvironment,
                    exeDir,
                    ref si,
                    out pi);

                if (!ok)
                {
                    int errorCode = Marshal.GetLastWin32Error();
                    Log.Error("CreateProcessAsUser 失败，错误码: {errorCode}", errorCode);
                    throw new Win32Exception(errorCode);
                }

                Log.Information("成功创建 ScreenCapture.exe 进程, Process ID: {pid}. 等待其初始化...", pi.dwProcessId);

                // 等待2秒，看进程是否意外退出
                uint waitResult = WaitForSingleObject(pi.hProcess, 2000);
                if (waitResult == 0x00000000) // WAIT_OBJECT_0, 进程已终止
                {
                    uint exitCode;
                    if (GetExitCodeProcess(pi.hProcess, out exitCode))
                    {
                        Log.Error("ScreenCapture.exe 进程在启动后迅速退出，退出代码: {exitCode} (十进制) / 0x{exitCode:X}", exitCode, exitCode);
                    }
                    else
                    {
                        Log.Error("ScreenCapture.exe 进程在启动后迅速退出，但无法获取其退出代码。");
                    }
                }
                else if (waitResult == 0x00000102) // WAIT_TIMEOUT
                {
                    Log.Information("ScreenCapture.exe 进程在2秒后仍在运行，可能已成功启动。");
                }
            }
            finally
            {
                if (pi.hThread != IntPtr.Zero) CloseHandle(pi.hThread);
                if (pi.hProcess != IntPtr.Zero) CloseHandle(pi.hProcess);
                if (lpEnvironment != IntPtr.Zero) DestroyEnvironmentBlock(lpEnvironment);
                if (hPrimaryToken != IntPtr.Zero) CloseHandle(hPrimaryToken);
                if (hImpersonationToken != IntPtr.Zero) CloseHandle(hImpersonationToken);
            }
        }

        #region P/Invoke Definitions

        [DllImport("kernel32.dll", SetLastError = true)]
        static extern uint WaitForSingleObject(IntPtr hHandle, uint dwMilliseconds);

        [DllImport("kernel32.dll", SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        static extern bool GetExitCodeProcess(IntPtr hProcess, out uint lpExitCode);

        [StructLayout(LayoutKind.Sequential)]
        private struct STARTUPINFO
        {
            public int cb;
            public string lpReserved;
            public string lpDesktop;
            public string lpTitle;
            public int dwX;
            public int dwY;
            public int dwXSize;
            public int dwYSize;
            public int dwXCountChars;
            public int dwYCountChars;
            public int dwFillAttribute;
            public int dwFlags;
            public short wShowWindow;
            public short cbReserved2;
            public IntPtr lpReserved2;
            public IntPtr hStdInput;
            public IntPtr hStdOutput;
            public IntPtr hStdError;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct PROCESS_INFORMATION
        {
            public IntPtr hProcess;
            public IntPtr hThread;
            public int dwProcessId;
            public int dwThreadId;
        }

        [StructLayout(LayoutKind.Sequential)]
        private struct SECURITY_ATTRIBUTES
        {
            public int nLength;
            public IntPtr lpSecurityDescriptor;
            public bool bInheritHandle;
        }

        private enum TOKEN_TYPE
        {
            TokenPrimary = 1,
            TokenImpersonation = 2
        }

        private enum SECURITY_IMPERSONATION_LEVEL
        {
            SecurityAnonymous = 0,
            SecurityIdentification = 1,
            SecurityImpersonation = 2,
            SecurityDelegation = 3
        }

        [Flags]
        private enum TOKEN_ACCESS_LEVEL : uint
        {
            STANDARD_RIGHTS_REQUIRED = 0x000F0000,
            STANDARD_RIGHTS_READ = 0x00020000,
            TOKEN_ASSIGN_PRIMARY = 0x0001,
            TOKEN_DUPLICATE = 0x0002,
            TOKEN_IMPERSONATE = 0x0004,
            TOKEN_QUERY = 0x0008,
            TOKEN_QUERY_SOURCE = 0x0010,
            TOKEN_ADJUST_PRIVILEGES = 0x0020,
            TOKEN_ADJUST_GROUPS = 0x0040,
            TOKEN_ADJUST_DEFAULT = 0x0080,
            TOKEN_ADJUST_SESSIONID = 0x0100,
            TOKEN_READ = (STANDARD_RIGHTS_READ | TOKEN_QUERY),
            TOKEN_ALL_ACCESS = (STANDARD_RIGHTS_REQUIRED | TOKEN_ASSIGN_PRIMARY |
                TOKEN_DUPLICATE | TOKEN_IMPERSONATE | TOKEN_QUERY | TOKEN_QUERY_SOURCE |
                TOKEN_ADJUST_PRIVILEGES | TOKEN_ADJUST_GROUPS | TOKEN_ADJUST_DEFAULT |
                TOKEN_ADJUST_SESSIONID),
            MAXIMUM_ALLOWED = 0x02000000
        }

        [DllImport("kernel32.dll", SetLastError = false)]
        private static extern uint WTSGetActiveConsoleSessionId();

        [DllImport("wtsapi32.dll", SetLastError = true)]
        private static extern bool WTSQueryUserToken(int sessionId, out IntPtr phToken);

        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool CloseHandle(IntPtr hObject);

        [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Auto)]
        private static extern bool CreateProcessAsUser(
            IntPtr hToken,
            string lpApplicationName,
            string lpCommandLine,
            IntPtr lpProcessAttributes,
            IntPtr lpThreadAttributes,
            bool bInheritHandles,
            int dwCreationFlags,
            IntPtr lpEnvironment,
            string lpCurrentDirectory,
            ref STARTUPINFO lpStartupInfo,
            out PROCESS_INFORMATION lpProcessInformation);

        [DllImport("advapi32.dll", SetLastError = true)]
        private static extern bool DuplicateTokenEx(
            IntPtr hExistingToken,
            uint dwDesiredAccess,
            ref SECURITY_ATTRIBUTES lpTokenAttributes,
            int ImpersonationLevel,
            int TokenType,
            out IntPtr phNewToken);

        [DllImport("userenv.dll", SetLastError = true)]
        private static extern bool CreateEnvironmentBlock(out IntPtr lpEnvironment, IntPtr hToken, bool bInherit);

        [DllImport("userenv.dll", SetLastError = true)]
        [return: MarshalAs(UnmanagedType.Bool)]
        private static extern bool DestroyEnvironmentBlock(IntPtr lpEnvironment);

        #endregion
    }
}