using Microsoft.AspNetCore.Mvc.RazorPages;

namespace AgentServer.Pages
{
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
    }
}