# Development targets. `herdr plugin link` skips the manifest's [[build]] step,
# so build first and then link the checkout.
GO ?= $(shell command -v go 2>/dev/null || echo $(HOME)/go-sdk/bin/go)
BUN ?= $(shell command -v bun 2>/dev/null)

.PHONY: build test test-go test-pi test-live e2e vet fmt probe integrations install-pi-extension daemon clean

build:
	sh scripts/build.sh

# Go unit tests (the live_* ones skip without a Herdr socket) plus the pi
# extension tests.
test: test-go test-pi

test-go:
	$(GO) test ./...

# The live_* tests talk to the Herdr instance running on this machine and skip
# when there is no socket. They are the only proof the wire types match the
# real server.
test-live:
	$(GO) test ./internal/adapters/herdr/ -run TestLive -v

test-pi:
ifeq ($(strip $(BUN)),)
	$(error bun not found; pi extension tests cannot run)
else
	$(BUN) test pi-extension/
endif

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

probe: build
	./bin/herdr-tg probe

integrations: build
	./bin/herdr-tg integrations

# Writes the emitter into ~/.pi/agent/extensions/. Needs Herdr's own pi
# integration too: herdr integration install pi
install-pi-extension: build
	./bin/herdr-tg install-extension

# End-to-end proof of the blocked chain. Mutates the machine: creates a tab,
# starts a real pi agent, installs a temporary pi extension and closes the pane.
# Requires the two pi extensions (see install-pi-extension) to be in place.
e2e:
	HERDR_E2E=1 \
	HERDR_E2E_FIXTURE=$(CURDIR)/pi-extension/testfixture/herdr-selftest.ts \
	HERDR_E2E_WORKSPACE=$${HERDR_E2E_WORKSPACE:-task} \
	$(GO) test ./internal/adapters/herdr/ -run TestE2EBlockedFromPi -v -timeout 5m

# Runs the bridge against the configured bot and group. Needs `herdr-tg
# setup` (or a hand-written config.json) first.
daemon: build
	./bin/herdr-tg daemon

clean:
	rm -rf bin
