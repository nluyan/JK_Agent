using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;

namespace AgentServer.Pages
{
    [Authorize]
    public class IndexModel : PageModel
    {
        private const int DefaultPageSize = 20;

        private readonly AgentService _agentService;

        public IndexModel(AgentService agentService)
        {
            _agentService = agentService;
        }

        public List<AgentModel> Agents { get; set; } = new();

        public int TotalCount { get; set; }

        public int FilteredCount { get; set; }

        public int CurrentPage { get; set; } = 1;

        public int PageSize { get; set; } = DefaultPageSize;

        public int TotalPages { get; set; } = 1;

        public string? IpFilter { get; set; }

        public string? SerialFilter { get; set; }

        public void OnGet(string? ip, string? sn, int pageNum = 1)
        {
            IpFilter = string.IsNullOrWhiteSpace(ip) ? null : ip.Trim();
            SerialFilter = string.IsNullOrWhiteSpace(sn) ? null : sn.Trim();

            var allAgents = _agentService.GetAgents();
            TotalCount = allAgents.Count;

            var filtered = allAgents.Where(a =>
                (IpFilter == null || (a.IpAddress ?? string.Empty).Contains(IpFilter, StringComparison.OrdinalIgnoreCase)) &&
                (SerialFilter == null || (a.BoardSerial ?? string.Empty).Contains(SerialFilter, StringComparison.OrdinalIgnoreCase)))
                .ToList();

            FilteredCount = filtered.Count;
            TotalPages = Math.Max(1, (filtered.Count + DefaultPageSize - 1) / DefaultPageSize);
            CurrentPage = Math.Clamp(pageNum, 1, TotalPages);
            Agents = filtered.Skip((CurrentPage - 1) * PageSize).Take(PageSize).ToList();
        }

        public async Task<IActionResult> OnPostAsync(string agentId, string handler)
        {
            if (handler == "RemoteManage")
            {
                try
                {
                    // 从配置中获取RemoteDesk的server和key参数
                    var configuration = HttpContext.RequestServices.GetRequiredService<IConfiguration>();

                    
                    var result = await _agentService.RemoteDesk(new RemoteDeskDTO { AgentId = agentId });
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