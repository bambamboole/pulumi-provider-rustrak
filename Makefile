BIN     := bin/pulumi-resource-rustrak
VERSION ?= $(shell node -p "require('./pulumi-plugin.json').version")
LDFLAGS := -X main.version=$(VERSION)
SDK_DIR := sdk/nodejs

.PHONY: build test vet lint fmt schema gen-sdk build-sdk install-local clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pulumi-resource-rustrak

test:
	go test ./...

vet:
	go vet ./...

# Requires golangci-lint v2: https://golangci-lint.run/welcome/install/
lint:
	golangci-lint run ./...

fmt:
	gofmt -w $$(git ls-files '*.go')

schema: build
	pulumi package get-schema ./$(BIN) > schema.json

# Regenerate the checked-in TypeScript SDK sources from the provider schema.
gen-sdk: build
	rm -rf $(SDK_DIR)
	pulumi package gen-sdk ./$(BIN) --language nodejs --out sdk
	cp LICENSE $(SDK_DIR)/LICENSE
	node patch-sdk.js
	cd $(SDK_DIR) && npm install --package-lock-only --ignore-scripts --no-audit --no-fund

# Compile the SDK into sdk/nodejs/bin, the directory that is published to npm
# and that local consumers reference with a file: dependency.
build-sdk:
	cd $(SDK_DIR) && npm ci --no-audit --no-fund && npm run build
	cp $(SDK_DIR)/package.json $(SDK_DIR)/README.md LICENSE $(SDK_DIR)/bin/
	cd $(SDK_DIR)/bin && node -e "const fs=require('fs');const p=JSON.parse(fs.readFileSync('package.json'));p.version='$(VERSION)';p.pulumi.version='$(VERSION)';delete p.scripts.prepare;delete p.devDependencies;fs.writeFileSync('package.json',JSON.stringify(p,null,2)+'\n')"

# Build the plugin and install it into the local Pulumi plugin cache so `pulumi`
# resolves it without a GitHub release.
install-local: build
	@pulumi plugin rm resource rustrak $(VERSION) --yes 2>/dev/null || true
	rm -rf bin/plugin
	mkdir -p bin/plugin
	cp $(BIN) bin/plugin/pulumi-resource-rustrak
	cp pulumi-plugin.json bin/plugin/pulumi-plugin.json
	tar czf bin/pulumi-resource-rustrak-$(VERSION).tar.gz -C bin/plugin pulumi-resource-rustrak pulumi-plugin.json
	pulumi plugin install resource rustrak $(VERSION) --file bin/pulumi-resource-rustrak-$(VERSION).tar.gz

clean:
	rm -rf bin $(SDK_DIR)/bin $(SDK_DIR)/node_modules schema.json
