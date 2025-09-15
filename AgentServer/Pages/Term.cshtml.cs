using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;

namespace AgentServer.Pages
{
    [Authorize]
    public class TermModel : PageModel
    {
		[BindProperty(SupportsGet = true)]
		public string Id { get; set; }

		public void OnGet()
        {
        }
    }
}
