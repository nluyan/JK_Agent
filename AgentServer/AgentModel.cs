namespace AgentServer
{
	public class AgentModel
	{
		public string IpAddress { get; set; }

		public string AgentId { get; set; }

		public string BoardSerial { get; set; }

		public string Version { get; set; }

		public int OSPlatform { get; set; }

		public string OSDescription { get; set; }

		public string OSArchitecture { get; set; }

		public string Group { get; set; }
	}
}
