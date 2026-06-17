# Fail if no target is provided
ifeq ($(MAKECMDGOALS),)
$(error No target specified. Look into the Makefile for available targets.)
endif

run_debug:
	go run ./cmd/gad/main.go -q ./queue.txt -d --browser

build:
	GOOS=linux GOARCH=amd64 go build -o gad-linux-x64 ./cmd/gad/main.go
	
build_windowstolinux:
	set GOOS=linux&& set GOARCH=amd64&& go build -o gad-linux-x64 ./cmd/gad/main.go
	copy gad-linux-x64 V:\Anime\gad-linux-x64
	del gad-linux-x64

build_share:
	GOOS=linux GOARCH=amd64 go build -o gad-linux-x64 ./cmd/gad/main.go
	mv gad-linux-x64 ~/media/Anime/gad-linux-x64

test:
	go test ./...

get_deps:
	go get -u ./...