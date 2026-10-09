# NetOps Lab — developer targets.
#
# Note on `build`: the main binary target (go build -o bin/netopslab
# ./cmd/netopslab) is intentionally NOT here yet — cmd/netopslab is wired by
# T7. Until then only testdatagen is buildable.

.PHONY: fmt vet test testdata build-testdatagen clean

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test -count=1 -timeout 10m ./...

testdata:
	go run ./cmd/testdatagen -out testdata

build-testdatagen:
	go build -o bin/testdatagen ./cmd/testdatagen

clean:
	rm -rf bin
