.PHONY: build test test-unit

build:
	go build -o sqlcli ./cmd/sqlcli

# Unit tests plus Oracle integration tests (credentials from test.env, if present)
test:
	@set -a; [ -f test.env ] && . ./test.env; set +a; go test -count=1 ./...

# Unit tests only (integration tests skip without SQLCLI_TEST_* variables)
test-unit:
	env -u SQLCLI_TEST_CONNECT go test -count=1 ./...
