using Microsoft.IdentityModel.JsonWebTokens;
using Microsoft.IdentityModel.Tokens;
using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

namespace AgentServer
{
	public class JwtService(IConfiguration configuration)
	{
		public string CreateToken(DateTime expireAt, 
			IList<object>? roles = null, 
			IList<object>? permissions = null, 
			IDictionary<string, object>? claims = null)
		{
			return CreateToken(
				o =>
				{
					o.SigningKey = configuration["Jwt:SecretKey"];
					o.ExpireAt = Convert.ToDateTime(expireAt);
					o.Issuer = configuration["Jwt:Issuer"];
					o.Audience = configuration["Jwt:Audience"];
					if (roles != null) o.User.Roles.AddRange(roles.Cast<string>());
					if (permissions != null) o.User.Permissions.AddRange(permissions.Cast<string>());
					if (claims != null)
					{
						foreach (var key in claims.Keys)
							if (claims[key] != null)
								o.User.Claims.Add(new Claim(key, claims[key].ToString()));
					}
				});
		}

		string CreateToken(Action<JwtCreationOptions> options)
		{
			JwtCreationOptions jwtCreationOptions = new JwtCreationOptions();
			options(jwtCreationOptions);
			if (string.IsNullOrEmpty(jwtCreationOptions.SigningKey))
			{
				throw new InvalidOperationException("SigningKey is required!");
			}

			List<Claim> list = new List<Claim>();
			if (jwtCreationOptions.User.Claims.Any())
			{
				list.AddRange(jwtCreationOptions.User.Claims);
			}

			if (jwtCreationOptions.User.Permissions.Any())
			{
				list.AddRange(jwtCreationOptions.User.Permissions.Select((p) => new Claim(SecurityOptions.PermissionsClaimType, p)));
			}

			if (jwtCreationOptions.User.Roles.Any())
			{
				list.AddRange(jwtCreationOptions.User.Roles.Select((r) => new Claim(SecurityOptions.RoleClaimType, r)));
			}

			SecurityTokenDescriptor tokenDescriptor = new SecurityTokenDescriptor
			{
				Issuer = jwtCreationOptions.Issuer,
				Audience = jwtCreationOptions.Audience,
				IssuedAt = TimeProvider.System.GetUtcNow().UtcDateTime,
				Subject = new ClaimsIdentity(list),
				Expires = jwtCreationOptions.ExpireAt,
				SigningCredentials = GetSigningCredentials(jwtCreationOptions)
			};
			return new JsonWebTokenHandler().CreateToken(tokenDescriptor);
			SigningCredentials GetSigningCredentials(JwtCreationOptions opts)
			{
				if (opts.SigningStyle == TokenSigningStyle.Asymmetric)
				{
					RSA rSA = RSA.Create();
					if (opts.KeyIsPemEncoded)
					{
						rSA.ImportFromPem(opts.SigningKey);
					}
					else
					{
						rSA.ImportRSAPrivateKey(Convert.FromBase64String(opts.SigningKey), out var _);
					}

					return new SigningCredentials(new RsaSecurityKey(rSA), opts.SigningAlgorithm);
				}

				return new SigningCredentials(new SymmetricSecurityKey(Encoding.ASCII.GetBytes(opts.SigningKey)), opts.SigningAlgorithm);
			}
		}
	}

	static class SecurityOptions
	{
		public static string NameClaimType { internal get; set; } = "name";

		public static string PermissionsClaimType { internal get; set; } = "permissions";

		public static string RoleClaimType { internal get; set; } = "role";

	}

	enum TokenSigningStyle
	{
		Symmetric,
		Asymmetric
	}

	class JwtCreationOptions
	{
		//
		// 摘要:
		//     the key used to sign jwts symmetrically or the private-key when jwts are signed
		//     asymmetrically.
		//
		// 言论：
		//     the key can be in PEM format. make sure to set FastEndpoints.Security.JwtCreationOptions.KeyIsPemEncoded
		//     to true if the key is PEM encoded.
		public string SigningKey { get; set; }

		//
		// 摘要:
		//     specifies how tokens are to be signed. symmetrically or asymmetrically.
		public TokenSigningStyle SigningStyle { get; set; }

		//
		// 摘要:
		//     security algo used to sign keys. defaults to HmacSha256 for symmetric keys and
		//     RsaSha256 for asymmetric keys.
		public string SigningAlgorithm { get; set; }

		//
		// 摘要:
		//     specifies whether the key is pem encoded.
		public bool KeyIsPemEncoded { get; set; }

		//
		// 摘要:
		//     specify the privileges of the user
		public UserPrivileges User { get; } = new UserPrivileges();


		//
		// 摘要:
		//     the value for the 'audience' claim.
		public string? Audience { get; set; }

		//
		// 摘要:
		//     the issuer
		public string? Issuer { get; set; }

		//
		// 摘要:
		//     the value of the 'expiration' claim. should be in utc.
		public DateTime? ExpireAt { get; set; }

		//
		// 摘要:
		//     the compression algorithm compressing the token payload.
		public string? CompressionAlgorithm { get; set; }

		public JwtCreationOptions()
		{
			SigningAlgorithm = SigningStyle == TokenSigningStyle.Symmetric ? "http://www.w3.org/2001/04/xmldsig-more#hmac-sha256" : "RS256";
		}
	}

	public sealed class UserPrivileges
	{
		//
		// 摘要:
		//     claims of the user
		public List<Claim> Claims { get; } = new List<Claim>();


		//
		// 摘要:
		//     roles of the user
		public List<string> Roles { get; } = new List<string>();


		//
		// 摘要:
		//     allowed permissions for the user
		public List<string> Permissions { get; } = new List<string>();


		//
		// 摘要:
		//     shortcut for adding a new System.Security.Claims.Claim to the claim list for
		//     the given claim type and value
		//
		// 参数:
		//   claimType:
		//     the claim type to add
		public string this[string claimType]
		{
			set
			{
				Claims.Add(new Claim(claimType, value));
			}
		}
	}
}
