using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;
using Microsoft.AspNetCore.SignalR;
using System.Text.Json;

namespace AgentServer.Pages
{
    [Authorize]
    public class ScriptModel : PageModel
    {
		private readonly AgentService _agentService;

        public ScriptModel(AgentService agentService)
        {
            _agentService = agentService;
        }

        [BindProperty(SupportsGet = true)]
        public string Id { get; set; }

        [BindProperty]
        public string ScriptContent { get; set; } = string.Empty;

        // 脚本编辑器支持的原生命令类型，为空时按普通 PowerShell 脚本下发。
        [BindProperty]
        public string? CommandType { get; set; }

        // 终端命令面板的两个表单直接绑定到要下发给 Agent 的 DTO。
        [BindProperty]
        public WatermarkDTO Watermark { get; set; } = new();

        [BindProperty]
        public WallpaperDTO Wallpaper { get; set; } = new();

        public string? ExecutionResult { get; set; }
        public string? ErrorMessage { get; set; }
        public bool IsExecuting { get; set; }

        public void OnGet()
        {
            // 检查Agent是否存在
            var agent = _agentService.GetById(Id);
            if (agent == null)
            {
                ErrorMessage = $"未找到Agent ID: {Id}";
            }
        }

        public async Task<IActionResult> OnPost()
        {
            // 调试信息
            Console.WriteLine($"OnPost called with Id: {Id}");
            Console.WriteLine($"ScriptContent from property: '{ScriptContent}'");
            
            // 尝试从 Request.Form 直接获取
            if (Request.Form.ContainsKey("ScriptContent"))
            {
                var formScriptContent = Request.Form["ScriptContent"].ToString();
                Console.WriteLine($"ScriptContent from form: '{formScriptContent}'");
                if (!string.IsNullOrWhiteSpace(formScriptContent))
                {
                    ScriptContent = formScriptContent;
                }
            }
            
            // 输出所有表单数据
            Console.WriteLine("All form data:");
            foreach (var key in Request.Form.Keys)
            {
                Console.WriteLine($"  {key}: {Request.Form[key]}");
            }
            
            if (string.IsNullOrWhiteSpace(ScriptContent))
            {
                ErrorMessage = "请输入PowerShell脚本内容";
                Console.WriteLine("ScriptContent is empty after all attempts");
                return Page();
            }

            var agent = _agentService.GetById(Id);
            if (agent == null)
            {
                ErrorMessage = $"Agent {Id} 未连接或不存在";
                Console.WriteLine($"Agent {Id} not found");
                return Page();
            }

            try
            {
                Console.WriteLine($"Executing script for Agent {Id}: {ScriptContent.Substring(0, Math.Min(50, ScriptContent.Length))}...");
                IsExecuting = true;
				var result = await _agentService.ExecutePowershellScript(new ExecuteDTO { AgentId = Id }, ScriptContent, CommandType);
				ExecutionResult = result.Result;
				Console.WriteLine($"Script execution completed. Result length: {ExecutionResult?.Length ?? 0}");
            }
            catch (Exception ex)
            {
                ErrorMessage = ex.Message;
                ExecutionResult = null;
                Console.WriteLine($"Script execution failed: {ex.Message}");
            }
            finally
            {
                IsExecuting = false;
            }

            return Page();
        }

        // 终端命令面板：屏幕水印。服务端不保存任何水印配置，命令下发后由终端自行持久化。
        public Task<IActionResult> OnPostWatermark() => RunAgentCommand("水印", () =>
        {
            Watermark.AgentId = Id;
            return _agentService.SetWatermark(Watermark);
        });

        // 终端命令面板：桌面壁纸。
        public Task<IActionResult> OnPostWallpaper() => RunAgentCommand("壁纸", () =>
        {
            Wallpaper.AgentId = Id;
            return _agentService.SetWallpaper(Wallpaper);
        });

        // 两个命令表单共用同一套“确认 Agent 在线 → 下发 → 回填结果”的流程。
        private async Task<IActionResult> RunAgentCommand(string label, Func<Task<ExecuteResult>> send)
        {
            if (_agentService.GetById(Id) == null)
            {
                ErrorMessage = $"Agent {Id} 未连接或不存在";
                return Page();
            }

            try
            {
                IsExecuting = true;
                var result = await send();
                ExecutionResult = result.Result;
                if (result.Status != 0)
                    ErrorMessage = $"{label}命令执行失败";
            }
            catch (Exception ex)
            {
                ErrorMessage = ex.Message;
                ExecutionResult = null;
            }
            finally
            {
                IsExecuting = false;
            }

            return Page();
        }
    }
}