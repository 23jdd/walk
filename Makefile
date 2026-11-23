BINARY := walk

.PHONY: build test race vet bench clean

build:
	go build -o $(BINARY) ./cmd/walk

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

bench:
	go test . -run '^$$' -bench . -benchmem

clean:
	rm -f $(BINARY) $(BINARY).exe coverage.out
