namespace AgentServer
{
	public class DTOBase
	{
		public string AgentId { get; set; }

		public string Serial { get; set; }

		public string Ip { get; set; }
	}

	public class ExecuteDTO : DTOBase
	{
		public string Script { get; set; }
	}

	public class ScreenDTO : DTOBase
	{
	}

	public class LoginDto
	{
		public string Username { get; set; }

		public string Password { get; set; }
	}
}
