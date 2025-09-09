using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;
using Microsoft.AspNetCore.SignalR;

namespace AgentServer.Pages
{
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
                ExecutionResult = await _agentService.ExecutePowershellScript(Id, ScriptContent);
                ErrorMessage = null;
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
    }
}