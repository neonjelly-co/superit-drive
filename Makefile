# Superit Drive. `make help` lists the targets.

BIN     := bin/superit-drive
IMAGE   := superit-drive:dev
STORAGE := superit-drive-dev-storage

.PHONY: help ui build notices test test-short test-memory image dev-storage run dev-ui check-storage

help:
	@grep -E '^[a-z-]+:.*## ' Makefile | sed 's/:.*## /\t/'

ui: ## Build the interface into web/dist (needs Bun)
	cd web && bun install --frozen-lockfile && bun run build

build: ui ## Build the Drive, interface included, into bin/
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/superit-drive

notices: ## Write THIRD-PARTY-NOTICES.txt again, after a dependency was added, removed or moved to another version
	cd web && bun install --frozen-lockfile
	go run ./dev/notices

test: ## Run every test (needs Docker running)
	cd web && bun test
	go test ./...

test-short: ## Run only the tests that need no Docker
	cd web && bun test
	go test -short ./...

test-memory: ## Run the image in a 64 MiB container under 40 downloads and 10 uploads of 256 MiB at once (slow; needs Docker)
	go test -tags memory -count=1 -v -timeout 30m ./test/memory/

image: ## Build the container image superit-drive:dev
	docker build -t $(IMAGE) .

dev-storage: ## Start a stand-in Drive storage (Apache mod_dav) on 127.0.0.1:8081
	docker build -q -t superit-drive-test-webdav test/webdav
	docker rm -f $(STORAGE) >/dev/null 2>&1 || true
	docker run -d --name $(STORAGE) -p 127.0.0.1:8081:80 superit-drive-test-webdav

run: build ## Run the Drive on 127.0.0.1:8080 with the settings in dev/drive.env
	set -a && . ./dev/drive.env && set +a && ./$(BIN)

dev-ui: ## Serve the interface with hot reload on :5173, signed in as DEV_USER_ID from dev/drive.env
	set -a && . ./dev/drive.env && set +a && cd web && bun run dev

check-storage: build ## Check the storage in dev/drive.env (the stand-in) as a WebDAV server is checked before a Drive
	set -a && . ./dev/drive.env && set +a && ./$(BIN) check-storage
