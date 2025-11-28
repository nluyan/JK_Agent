$env:CGO_ENABLED = "0"

$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o output/win64/AgentClient.exe

$env:GOOS = "windows"
$env:GOARCH = "386"
go build -o output/win32/AgentClient.exe

$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o output/linux/AgentClient