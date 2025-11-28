module jkagent/goclient

go 1.20

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/kardianos/service v1.2.2
	github.com/philippseith/signalr v0.0.0
	github.com/rs/zerolog v1.34.0
)

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/coder/websocket v1.8.13 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/teivah/onecontext v1.3.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
)

replace github.com/philippseith/signalr => ./signalr

// Force compatible versions for Windows 7 (Go 1.20)
replace github.com/kardianos/service => github.com/kardianos/service v1.2.2

replace github.com/quic-go/quic-go => github.com/quic-go/quic-go v0.40.1

replace github.com/quic-go/webtransport-go => github.com/quic-go/webtransport-go v0.6.0

replace github.com/quic-go/qpack => github.com/quic-go/qpack v0.4.0

replace golang.org/x/crypto => golang.org/x/crypto v0.17.0

replace golang.org/x/text => golang.org/x/text v0.14.0

replace golang.org/x/net => golang.org/x/net v0.19.0

replace golang.org/x/sys => golang.org/x/sys v0.15.0

replace golang.org/x/mod => golang.org/x/mod v0.14.0

replace golang.org/x/tools => golang.org/x/tools v0.16.0

replace golang.org/x/sync => golang.org/x/sync v0.5.0

replace go.uber.org/mock => go.uber.org/mock v0.4.0
