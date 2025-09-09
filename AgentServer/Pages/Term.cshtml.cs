using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;

namespace AgentServer.Pages
{
    public class TermModel : PageModel
    {
		[BindProperty(SupportsGet = true)]
		public string Id { get; set; }

		public void OnGet()
        {
        }
    }
}
