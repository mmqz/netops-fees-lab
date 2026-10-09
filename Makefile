# NetOps Lab — developer targets.

.PHONY: fmt vet test testdata build build-testdatagen web clean

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test -count=1 -timeout 15m ./...

testdata:
	go run ./cmd/testdatagen -out testdata

# The single NetOps Lab binary (serve/import/analyze/validate/report/query/mcp/testdata).
build:
	go build -o bin/netopslab ./cmd/netopslab

build-testdatagen:
	go build -o bin/testdatagen ./cmd/testdatagen

# Frontend build: installs web/ dependencies and builds the React SPA into
# internal/webui/dist (the embedded assets served by the Go binary).
# Node 24 / npm 11 required; the Go build embeds whatever is in dist/.
web:
	cd web && npm ci && npm run build

clean:
	rm -rf bin
