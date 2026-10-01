# Fail if no target is provided
ifeq ($(MAKECMDGOALS),)
$(error No target specified. Look into the Makefile for available targets.)
endif

run_debug:
	go run ./cmd/gad/main.go -q ./queue.txt -d --browser

build:
	GOOS=linux GOARCH=amd64 go build -o gad-linux-x64 ./cmd/gad/main.go
	
build_windowstolinux:
	set GOOS=linux&& set GOARCH=amd64 && go build -o gad-linux-x64 ./cmd/gad/main.go
	copy gad-linux-x64 V:\Anime\gad-linux-x64
	del gad-linux-x64

build_share:
	GOOS=linux GOARCH=amd64 go build -o gad-linux-x64 ./cmd/gad/main.go
	mv gad-linux-x64 ~/media/Anime/gad-linux-x64
	
	GOOS=linux GOARCH=amd64 go build -o sort-linux-x64 ./cmd/sort/main.go
	GOOS=windows GOARCH=amd64 go build -o sort-windows-x64.exe ./cmd/sort/main.go
	mv sort-linux-x64 ~/media/Anime/sort-linux-x64
	mv sort-windows-x64.exe ~/media/Anime/sort-windows-x64.exe

	GOOS=linux GOARCH=amd64 go build -o manager-linux-x64 ./cmd/libraryManager/main.go
	GOOS=windows GOARCH=amd64 go build -o manager-windows-x64.exe ./cmd/libraryManager/main.go
	mv manager-linux-x64 ~/media/Anime/manager-linux-x64
	mv manager-windows-x64.exe ~/media/Anime/manager-windows-x64.exe

test:
	go test ./...

get_deps:
	go get -u ./...

build_phone:
	GOOS=android GOARCH=arm64 go build -o gad-android-arm64 ./cmd/gad/main.go