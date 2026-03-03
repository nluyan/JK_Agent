# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

AgentServer is a remote agent management and terminal control server built with **ASP.NET Core 10.0**. It enables real-time communication with remote agents via SignalR, supports PowerShell script execution, and provides a web-based terminal UI using xterm.js.

## Build Commands

```bash
# Debug build
dotnet build

# Release build
dotnet build -c Release

# Run locally (http://localhost:17037)
dotnet run

# Publish for Windows
dotnet publish -c Release -r win-x64 --self-contained false

# Publish for Linux
dotnet publish -c Release -r linux-x64 --self-contained false

# Install as systemd service (Linux)
./AgentServer --install

# Uninstall service
./AgentServer --uninstall
```

## Architecture

### Component Flow

```
Browser Client (xterm.js, SignalR)
        │
        ▼
┌───────────────────┐
│  ASP.NET Core     │  Program.cs configures:
│  Server           │  - HTTP/HTTPS endpoints
│                   │  - SignalR Hub (/AgentHub)
│                   │  - JWT + Cookie auth
└─────────┬─────────┘
          │
          ▼
┌───────────────────┐     ┌─────────────────┐
│  AgentHub         │────▶│  AgentService   │
│  (SignalR Hub)    │     │  (Agent registry│
│                   │     │   & operations) │
└─────────┬─────────┘     └────────┬────────┘
          │                        │
          ▼                        ▼
    Connected Agents        JWT Service
    (clients calling        (token generation)
    RegisterAgent,
    UpdateAgent, etc.)
```

### Key Components

| File | Purpose |
|------|---------|
| [AgentHub.cs](AgentHub.cs) | SignalR hub managing real-time agent connections. Handles registration, commands, terminal I/O via methods like `RegisterAgent()`, `UpdateAgent()`, `ExecutePowerShell()`, `SendInput()` |
| [AgentService.cs](AgentService.cs) | Manages in-memory agent registry (`ConcurrentDictionary`). Provides REST API endpoints and executes remote PowerShell scripts |
| [JwtService.cs](JwtService.cs) | Generates JWT tokens for API authentication. Supports HMAC and RSA signing |
| [AgentModel.cs](AgentModel.cs) | Agent data model (AgentId, IP, OS info, hostname, group) |
| [DTO.cs](DTO.cs) | Request/response DTOs: `ExecuteDTO`, `RemoteDeskDTO`, `LoginDto`, `ExecuteResult` |

### API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/login` | None | Admin login (cookie-based) |
| GET | `/api/agent/list` | JWT | List all connected agents |
| POST | `/api/agent/execute` | JWT | Execute PowerShell script |
| POST | `/api/agent/remotedes` | JWT | Initiate remote desktop |

### SignalR Protocol

Agents connect to `/AgentHub` and use MessagePack protocol. Key methods:
- **Agent → Server**: `RegisterAgent()`, `UpdateAgent()`, `PowerShellOutput()`
- **Server → Agent**: `ExecutePowerShell()`, `SendInput()`, `RemoteDesk()`

## Configuration

Configuration is in [appsettings.json](appsettings.json):
- `Port:Http` / `Port:Https` - Server ports (17037/17038)
- `Jwt` - Token issuer, authority, audience
- `Admin` - Web UI credentials
- `RemoteDeskServers` - Remote desktop server mappings
- `cmdbApi` - CMDB API endpoint for agent registration

## Notes

- Chinese comments and configuration values exist in parts of the codebase
- Dual authentication: Cookie (web UI) + JWT Bearer (API)
- Agent registry is in-memory (`ConcurrentDictionary<string, AgentModel>`)
