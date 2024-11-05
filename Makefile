tidy:
	go mod tidy
fmt: tidy
	go fmt ./...
update:
	go get -u ./...
test:
	go test ./...
lint:
	golangci-lint run
