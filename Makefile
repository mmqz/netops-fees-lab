# NetOps Fees Lab — developer targets.

.PHONY: fmt vet test testdata build build-duckdb build-testdatagen web demo all clean

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# The Go test suite embeds internal/webui/dist via go:embed; three SPA
# smoke tests fail with a bare 404 when dist has not been built (issue #13).
# Pre-check instead of running the suite into a confusing red: the error
# below names the one command that fixes it. A fresh clone therefore gets
# a clear instruction instead of 3 x 404 test failures.
test:
	@test -f internal/webui/dist/index.html || \
	  (echo "ERROR: 前端未构建——internal/webui/dist 为空，SPA 相关测试将 404 失败（issue #13）。"; \
	   echo "       请先运行: make web （需要 Node 18+；构建后重跑本目标即可全绿）"; exit 1)
	go test -count=1 -timeout 15m ./...

testdata:
	go run ./cmd/testdatagen -out testdata

# The single NetOps Fees Lab binary (serve/import/analyze/ingest/metrics/validate/report/fees/mcp/testdata).
# Embeds whatever is currently in internal/webui/dist — run `make web` first
# (or `make all`) to bundle the real frontend; without it the SPA is a stub.
# BIN_EXT appends ".exe" on Windows (issue #3: the e2e tests and `make build`
# must produce a runnable name on every platform).
BIN_EXT := $(shell go env GOOS | grep -s windows && echo .exe || echo)
build:
	go build -o bin/netopsfees$(BIN_EXT) ./cmd/netopsfees

# The DuckDB-engine build (#65 P1-c): CGO + gcc + `-tags duckdb` compiles
# the fee-analysis query engine in (issue #65's Windows finding: mingw-w64
# lacks rstrtmgr, so this target is LINUX/WSL2/macOS only — Windows users
# get the query-capable binary via `docker build` or WSL2, see
# docs/fees-layer.md §5). The default `make build` stays CGO-free and
# answers 501 engine_not_built on fee queries.
build-duckdb:
	CGO_ENABLED=1 go build -tags duckdb -o bin/netopsfees-duckdb ./cmd/netopsfees

build-testdatagen:
	go build -o bin/testdatagen$(BIN_EXT) ./cmd/testdatagen

# Frontend build: installs web/ dependencies and builds the React SPA into
# internal/webui/dist (the embedded assets served by the Go binary).
# Node 24 / npm 11 required; the Go build embeds whatever is in dist/.
web:
	cd web && npm ci && npm run build

# Repeatable demo: fresh temp DB → import×3 → validate → HTML report → stats.
# Uses bin/netopsfees (builds it first if missing). See scripts/demo.sh.
demo:
	bash scripts/demo.sh


# Full production artifact: frontend first, then the binary that embeds it.
all: web build

clean:
	rm -rf bin
