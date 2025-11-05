REPO_ROOT:=${CURDIR}

## @ Code Generation Variables

# Find the code-generator package in the Go module cache.
# The 'go mod download' command in the targets ensures this will succeed.
CODEGEN_PKG = $(shell go env GOPATH)/pkg/mod/k8s.io/code-generator@$(shell go list -m -f '{{.Version}}' k8s.io/code-generator)
CODEGEN_SCRIPT := $(CODEGEN_PKG)/kube_codegen.sh

# The root directory where your API type definitions are located.
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/
# The root directory where client code will be placed.
CLIENT_OUTPUT_DIR := $(REPO_ROOT)/k8s/client
# The root Go package for your generated client code.
CLIENT_OUTPUT_PKG := $(shell go list -m)/k8s/client
BOILERPLATE_FILE := hack/boilerplate.go.txt


## @ Code Generation

.PHONY: generate-all
generate-all: manifests deepcopy clientsets ## Generate manifests, deepcopy code, and clientsets.

.PHONY: manifests
manifests: controller-gen ## Generate CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths="./api/..." output:crd:artifacts:config=k8s/crds

.PHONY: deepcopy
deepcopy: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="$(BOILERPLATE_FILE)" paths="./api/..."

.PHONY: clientsets
clientsets: ## Generate clientsets, listers, and informers.
	@echo "--- Ensuring code-generator is in module cache..."
	@go mod download k8s.io/code-generator
	@echo "+++ Generating client code..."
	@bash -c 'source $(CODEGEN_SCRIPT); \
		kube::codegen::gen_client \
		    --with-watch \
		    --output-dir $(CLIENT_OUTPUT_DIR) \
		    --output-pkg $(CLIENT_OUTPUT_PKG) \
		    --boilerplate $(BOILERPLATE_FILE) \
		    ./api/'


## @ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.19.0

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $$(realpath $(1)-$(3)) $(1)
endef
