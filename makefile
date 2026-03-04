# Fail if no target is provided
ifeq ($(MAKECMDGOALS),)
$(error No target specified. Available targets: run_debug, build_linux)
endif

run_debug:
	go run ./cmd/gad/main.go -q ./queue.txt -d

build_ll:
	GOOS=linux GOARCH=amd64 go build -o gad-linux-x64 ./cmd/gad/main.go
	
build_winl:
	set GOOS=linux&& set GOARCH=amd64&& go build -o gad-linux-x64 ./cmd/gad/main.go
	copy gad-linux-x64 V:\Anime\gad-linux-x64
	del gad-linux-x64