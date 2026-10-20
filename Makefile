# NetOps Lab — developer targets.

.PHONY: fmt vet test testdata build build-duckdb build-testdatagen web demo demo-lab run-win all clean

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test -count=1 -timeout 15m ./...

testdata:
	go run ./cmd/testdatagen -out testdata

# The single NetOps Lab binary (serve/import/analyze/validate/report/query/mcp/testdata).
# Embeds whatever is currently in internal/webui/dist — run `make web` first
# (or `make all`) to bundle the real frontend; without it the SPA is a stub.
# BIN_EXT appends ".exe" on Windows (issue #3: the e2e tests and `make build`
# must produce a runnable name on every platform).
BIN_EXT := $(shell go env GOOS | grep -s windows && echo .exe || echo)
build:
	go build -o bin/netopslab$(BIN_EXT) ./cmd/netopslab

# The DuckDB-engine build (#65 P1-c): CGO + gcc + `-tags duckdb` compiles
# the fee-analysis query engine in (issue #65's Windows finding: mingw-w64
# lacks rstrtmgr, so this target is LINUX/WSL2/macOS only — Windows users
# get the query-capable binary via `docker build` or WSL2, see
# docs/fees-layer.md §5). The default `make build` stays CGO-free and
# answers 501 engine_not_built on fee queries.
build-duckdb:
	CGO_ENABLED=1 go build -tags duckdb -o bin/netopslab-duckdb ./cmd/netopslab

build-testdatagen:
	go build -o bin/testdatagen$(BIN_EXT) ./cmd/testdatagen

# Frontend build: installs web/ dependencies and builds the React SPA into
# internal/webui/dist (the embedded assets served by the Go binary).
# Node 24 / npm 11 required; the Go build embeds whatever is in dist/.
web:
	cd web && npm ci && npm run build

# Repeatable demo: fresh temp DB → import×3 → validate → HTML report → stats.
# Uses bin/netopslab (builds it first if missing). See scripts/demo.sh.
demo:
	bash scripts/demo.sh

# Repeatable simulation-workbench demo: fresh temp DB → import×3 →
# materialize workspace → fault injection (CHK-001 + link down) → path/diff
# → undo → checkpoint create/break/restore. See scripts/demo-lab.sh.
demo-lab:
	bash scripts/demo-lab.sh

# Windows one-click run: frontend -> binary -> realdata import -> serve -> browser.
# Equivalent of `make demo-real` for Windows (the .sh scripts need bash).
# See run.ps1 and docs/run-windows.md.
run-win:
	pwsh -NoProfile -ExecutionPolicy Bypass -File ./run.ps1

# Full production artifact: frontend first, then the binary that embeds it.
all: web build

clean:
	rm -rf bin
