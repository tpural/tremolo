VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
IMAGE   ?= ghcr.io/tpural/tremolo
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build web test lint image chart deps dev

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/tremolo ./cmd/tremolo

web:
	cd web && npm ci --no-audit --no-fund && npm run build

test:
	go test -race ./...
	cd web && npm test

lint:
	golangci-lint run
	helm lint charts/tremolo-kubernetes-collector charts/tremolo

image:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

lint chart: deps

deps:
	helm dependency build charts/tremolo-kubernetes-collector
	helm dependency update charts/tremolo

# Both charts, at the same version.
chart:
	sed -i 's/^version: .*/version: $(VERSION)/; s/^appVersion: .*/appVersion: "$(VERSION)"/' charts/tremolo-kubernetes-collector/Chart.yaml
	helm dependency update charts/tremolo
	helm package charts/tremolo-kubernetes-collector -d dist
	helm package charts/tremolo --version $(VERSION) --app-version $(VERSION) -d dist

# Run the API against a cluster's Victoria services: make dev, then cd web && npm run dev.
dev:
	go run ./cmd/tremolo --listen :8080 \
		--metrics-url http://localhost:8428 --logs-url http://localhost:9428
