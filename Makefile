default:
	go install -trimpath -ldflags='-extldflags=-static -s -w' ./...
	upx  ~/go/bin/blow
linux:
	GOOS=linux GOARCH=amd64 go install -trimpath -ldflags='-extldflags=-static -s -w' ./...
	upx  ~/go/bin/linux_amd64/blow

