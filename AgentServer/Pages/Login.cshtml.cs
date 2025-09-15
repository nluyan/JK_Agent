using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.RazorPages;
using System.Security.Claims;

namespace AgentServer.Pages
{
    public class LoginModel : PageModel
    {
        private readonly IConfiguration _configuration;

        public LoginModel(IConfiguration configuration)
        {
            _configuration = configuration;
        }

        [BindProperty]
        public string Username { get; set; } = string.Empty;

        [BindProperty]
        public string Password { get; set; } = string.Empty;

        public string ErrorMessage { get; set; } = string.Empty;
        public string SuccessMessage { get; set; } = string.Empty;

        public void OnGet()
        {
            // 如果用户已经登录，重定向到首页
            if (User.Identity.IsAuthenticated)
            {
                Response.Redirect("/");
            }
        }

        public async Task<IActionResult> OnPostAsync()
        {
            if (!ModelState.IsValid)
            {
                ErrorMessage = "请填写完整的登录信息";
                return Page();
            }

            var adminUsername = _configuration["Admin:Username"];
            var adminPassword = _configuration["Admin:Password"];

            if (Username == adminUsername && Password == adminPassword)
            {
                // 创建身份认证的Claims
                var claims = new List<Claim>
                {
                    new Claim(ClaimTypes.Name, Username),
                    new Claim(ClaimTypes.NameIdentifier, Username),
                    new Claim("AdminUser", "true")
                };

                // 创建身份认证的Identity
                var identity = new ClaimsIdentity(claims, "Cookies");
                var principal = new ClaimsPrincipal(identity);

                // 登录用户（设置Cookie）
                await HttpContext.SignInAsync("Cookies", principal, new AuthenticationProperties
                {
                    IsPersistent = true, // 持久化Cookie
                    ExpiresUtc = DateTimeOffset.UtcNow.AddDays(30)
                });

                SuccessMessage = "登录成功，正在跳转...";
                
                // 延迟重定向，让用户看到成功信息
                Response.Headers.Append("Refresh", "1; url=/");
                return Page();
            }
            else
            {
                ErrorMessage = "用户名或密码错误";
                return Page();
            }
        }

        public async Task<IActionResult> OnPostLogoutAsync()
        {
            await HttpContext.SignOutAsync("Cookies");
            return RedirectToPage("/Login");
        }
    }
}