$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o output/AgentClient64.exe

$env:GOOS = "windows"
$env:GOARCH = "386"
go build -o output/AgentClient32.exe

$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o output/AgentClient