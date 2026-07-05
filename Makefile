.PHONY: build run test vet docker

build:
	go build -o bin/monitorkube ./cmd/monitorkube

run: build
	./bin/monitorkube

test:
	go test ./...

vet:
	go vet ./...

docker:
	docker build -t monitorkube:dev .
