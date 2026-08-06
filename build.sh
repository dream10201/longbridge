rm -rf longbridge
go get -u ./...
go mod tidy
go build -ldflags="-s -w" -o longbridge
