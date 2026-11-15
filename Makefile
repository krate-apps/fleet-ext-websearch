.PHONY: build pack
build:
	mkdir -p bin
	go build -trimpath -o bin/websearch-worker ./cmd/websearch-worker

pack:
	bash scripts/pack.sh dist
