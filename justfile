export GOEXPERIMENT := "jsonv2"

build:
	go build ./cmd/yomiagen

test:
	go test ./...

run *args:
	go run ./cmd/yomiagen -- {{args}}

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

engine:
	docker run --rm -p 50021:50021 voicevox/voicevox_engine:cpu-latest
