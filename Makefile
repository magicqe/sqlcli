.PHONY: build build-windows test test-unit

build:
	go build -o sqlcli ./cmd/sqlcli

# Windows (64-bit) build: sqlcli.exe. godror needs cgo, so this needs a C
# compiler for Windows: mingw-w64 (Debian/Ubuntu: apt install gcc-mingw-w64-x86-64)
# or e.g. WINDOWS_CC="zig cc -target x86_64-windows-gnu". The Oracle client
# isn't needed to build; sqlcli.exe loads oci.dll from the Instant Client at run time.
WINDOWS_CC ?= x86_64-w64-mingw32-gcc

build-windows:
	@command -v $(firstword $(WINDOWS_CC)) >/dev/null || { \
		echo "$(firstword $(WINDOWS_CC)) not found: install mingw-w64 (apt install gcc-mingw-w64-x86-64) or set WINDOWS_CC"; exit 1; }
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="$(WINDOWS_CC)" go build -o sqlcli.exe ./cmd/sqlcli

# Unit tests plus Oracle integration tests (credentials from test.env, if present)
test:
	@set -a; [ -f test.env ] && . ./test.env; set +a; go test -count=1 ./...

# Unit tests only (integration tests skip without SQLCLI_TEST_* variables)
test-unit:
	env -u SQLCLI_TEST_CONNECT go test -count=1 ./...
