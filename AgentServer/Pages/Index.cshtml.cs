using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;

namespace AgentServer.Pages
{
    [Authorize]
    public class IndexModel : PageModel
    {
        private readonly AgentService _agentService;

        public IndexModel(AgentService agentService)
        {
            _agentService = agentService;
        }

        public List<AgentModel> Agents { get; set; } = new();

        public void OnGet()
        {
            Agents = _agentService.GetAgents();
        }

        public async Task<IActionResult> OnPostAsync(string agentId, string handler)
        {
            if (handler == "RemoteManage")
            {
                try
                {
                    // 从配置中获取RemoteDesk的server和key参数
                    var configuration = HttpContext.RequestServices.GetRequiredService<IConfiguration>();
                    var server = configuration["RemoteDesk:Server"];
                    var key = configuration["RemoteDesk:Key"];
                    
                    var result = await _agentService.RemoteDesk(new RemoteDeskDTO { AgentId = agentId }, server, key);
                    return new JsonResult(new { success = true, data = result });
                }
                catch (Exception ex)
                {
                    return new JsonResult(new { success = false, error = ex.Message });
                }
            }
            else if (handler == "Logout")
            {
                await HttpContext.SignOutAsync("Cookies");
                return RedirectToPage("/Login");
            }

            return new JsonResult(new { success = false, error = "Unknown handler" });
        }
    }
}