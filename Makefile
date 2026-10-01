IMG ?= quay.io/redhat-et/pega-operator:latest
BUNDLE_IMG ?= quay.io/redhat-et/pega-operator-bundle:latest

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
KUSTOMIZE ?= $(LOCALBIN)/kustomize
LOCALBIN ?= $(shell pwd)/bin

CONTROLLER_TOOLS_VERSION ?= v0.16.5
KUSTOMIZE_VERSION ?= v5.5.0

.PHONY: all
all: generate fmt vet build

##@ Development

.PHONY: manifests
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=pega-operator-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: manifests generate fmt vet
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: manifests generate fmt vet
	go build -o bin/manager main.go

.PHONY: run
run: manifests generate fmt vet
	go run ./main.go

.PHONY: docker-build
docker-build:
	podman build -t ${IMG} .

.PHONY: docker-push
docker-push:
	podman push ${IMG}

##@ Deployment

.PHONY: install
install: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found -f -

.PHONY: deploy
deploy: manifests kustomize
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found -f -

##@ OLM Bundle

.PHONY: bundle
bundle: manifests kustomize
	operator-sdk generate kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | operator-sdk generate bundle -q --overwrite --version $(VERSION)
	operator-sdk bundle validate ./bundle

.PHONY: bundle-build
bundle-build:
	podman build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push:
	podman push $(BUNDLE_IMG)

##@ Tool Dependencies

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: kustomize
kustomize: $(KUSTOMIZE)
$(KUSTOMIZE): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
