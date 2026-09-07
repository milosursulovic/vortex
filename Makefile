.PHONY: build run test vet fmt tidy race bench

build:
	go build -o bin/vortex ./cmd/vortex

run: build
	./bin/vortex -config configs/vortex.yaml

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -bench=. ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy
