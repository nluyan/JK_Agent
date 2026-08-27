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
		public bool Native { get; set; }
		public string Script { get; set; }
	}

	public class ScreenDTO : DTOBase { }

	public class RemoteDeskDTO : DTOBase { }

	public class PrinterDriverInstallDTO : DTOBase
	{
		public string DriverUrl { get; set; }
	}

	public class LoginDto
	{
		public string Username { get; set; }

		public string Password { get; set; }
	}

	public class ExecuteResult
	{
		public int Status { get; set; }

		public string? Result { get; set; }
	}
}
