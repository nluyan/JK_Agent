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
            if (handler == "CaptureScreen")
            {
                try
                {
                    var imageData = await _agentService.CaptureScreen(agentId);
                    return new FileContentResult(imageData, "image/jpg")
                    {
                        FileDownloadName = $"screenshot_{agentId}_{DateTime.Now:yyyyMMdd_HHmmss}.jpg"
                    };
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