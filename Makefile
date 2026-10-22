.PHONY: build run test proto fmt dev

build:
	go build -trimpath -ldflags="-s -w" -o bin/ktms-admin ./cmd/ktms-admin

test:
	go test -race ./...

# Regenerate internal/gen from proto/ (needs buf).
proto:
	buf generate

fmt:
	go fmt ./...

# Local run without Google sign-in (localhost only). Needs KTMS_ADDR + KTMS_ADMIN_KEY and/or TOKENMETER_URL.
dev:
	PUBLIC_URL=http://localhost:8080 INSECURE_NO_AUTH=true go run ./cmd/ktms-admin
