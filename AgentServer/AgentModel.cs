namespace AgentServer
{
	public class AgentModel
	{
		public string IpAddress { get; set; }

		public string AgentId { get; set; }

		public string BoardSerial { get; set; }

		public string Version { get; set; }

		public int OSPlatform { get; set; } //1:windows 2:linux 3:macos 0:unknown

		public string OSDescription { get; set; } //Windows 10 Pro 22H2 19045.3448

		public string OSArchitecture { get; set; } //x64, ARM64, x86

		public string Group { get; set; }

		public string HostName { get; set; }
	}
}
