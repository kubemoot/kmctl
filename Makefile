BINARY  := kmctl
MODULE  := github.com/kubemoot/kmctl
VERSION ?= dev
LDFLAGS := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)

LOCALBIN := $(shell pwd)/bin
GOLANGCI_LINT := $(LOCALBIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.14.0

.PHONY: build
build: ## Build the kmctl binary
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

.PHONY: test
test: ## Run unit tests with coverage
	go test -coverprofile=cover.out ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector (requires CGO)
	CGO_ENABLED=1 go test -race ./...

ENVTEST := $(LOCALBIN)/setup-envtest
ENVTEST_VERSION ?= release-0.21
ENVTEST_K8S_VERSION ?= 1.33

.PHONY: setup-envtest
setup-envtest: $(LOCALBIN) ## Install setup-envtest + the apiserver/etcd binaries
	@test -x $(ENVTEST) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

.PHONY: test-integration
test-integration: setup-envtest ## Tier 2 drift tests vs current CRDs (set KMCTL_CRD_DIR)
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test -tags=integration ./internal/integration/...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint (CC<=10, dupl, fmt)
	$(GOLANGCI_LINT) run

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: hooks
hooks: ## Install the git pre-commit hook
	git config core.hooksPath .githooks

.PHONY: golangci-lint
golangci-lint: $(LOCALBIN) ## Install golangci-lint into ./bin if missing
	@test -x $(GOLANGCI_LINT) || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'
