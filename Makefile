# KARTIKA-AGENT — target build
BINARY := kartika-agent
LDFLAGS := -s -w

.PHONY: build dist run test clean fmt vet

# Build binary untuk platform lokal
build:
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) .

# Cross-compile seluruh binary distribusi (LAPIS 2)
dist:
	bash build.sh

# Contoh menjalankan misi dari source
run:
	go run . --mission "Audit direktori kerja saat ini dan susun SITREP" --no-mcp

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin dist
