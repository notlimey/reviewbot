---
name: kubernetes-makefile
description: "Makefiles for Kubernetes operator dev — deploy/undeploy/test targets, kind clusters, kubectl context safety, golangci-lint version pinning. Not for general Make or non-K8s shell scripts."
paths:
  - "**/Makefile"
  - "**/*.mk"
---

# Makefile & Local Development

The Makefile is also a foot-gun magazine. `make deploy` against the wrong cluster ruins someone's afternoon, or someone's quarter. Guard the destructive targets explicitly.

## Core Rules

**Safeguard destructive targets with a context check.** Add a small target — convention `on-kind` — that fails unless the current `kubectl` context points at a local kind cluster. Make `deploy`, `undeploy`, and anything else that mutates a cluster depend on it.

```makefile
KIND_CONTEXT ?= kind-kind

.PHONY: on-kind
on-kind:
	@current=$$(kubectl config current-context); \
	if [ "$$current" != "$(KIND_CONTEXT)" ]; then \
		echo "refusing to run: current context is '$$current', expected '$(KIND_CONTEXT)'"; \
		exit 1; \
	fi

.PHONY: deploy
deploy: on-kind
	kubectl --context $(KIND_CONTEXT) apply -k kustomize/local

.PHONY: undeploy
undeploy: on-kind
	kubectl --context $(KIND_CONTEXT) delete -k kustomize/local
```

**Pass `--context kind-kind` (or your variable) to every `kubectl` invocation in the Makefile.** Belt and suspenders: the context-check target catches the obvious mistake; explicit `--context` survives a future contributor who removes the guard.

```makefile
# Good
kubectl --context $(KIND_CONTEXT) apply -f manifests/

# Bad — runs against whatever context is current at exec time
kubectl apply -f manifests/
```

**Pin the Go linter version in `.golangci.toml`, not in CI scripts.** Local `make lint` and CI lint must agree, and the only way to guarantee that is one source of truth checked into the repo.

```toml
# .golangci.toml
[run]
go = "1.23"
timeout = "5m"

[linters]
enable = ["errcheck", "govet", "staticcheck", "ineffassign"]
```

CI installs the version from a file the dev also uses (`golangci-lint --version` matches what `make lint` runs). No hardcoded version in `.github/workflows/*.yaml`.

## Patterns to Reach For

**Phony targets declared.** `.PHONY: deploy undeploy test lint` — without it, a file named `deploy` will silently break the target.

**Variables for everything that varies.** Cluster name, namespace, image tag, registry. Easier to override per-environment than to grep-replace.

**Help target.** A `make help` that grep-parses targets and their `## comment` lines pays back fast.

```makefile
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
```

**Tooling installed under a project-local `bin/`.** `bin/golangci-lint`, `bin/controller-gen`, `bin/kustomize` — pinned versions, not whatever's on `$PATH`.

## Patterns to Avoid

**Don't write `kubectl` commands without `--context` in any target that mutates state.** If a contributor runs the target with their `prod-eu1` context active, you've shipped a production change.

**Don't `cd` between commands inside a recipe** without `&&`-chaining. Each line in a Make recipe runs in its own shell; `cd` from line 1 doesn't carry to line 2.

**Don't shell out to `go install` for tools at runtime** without pinning a version. Reproducibility goes out the window the moment upstream cuts a release that breaks your build.

**Don't make `test` depend on the cluster.** Unit tests should run without a kubectl context at all. Use a separate `test-integration` target for things that need a cluster.

## Mandatory Checks Before Shipping

1. **`deploy` and `undeploy` (and any cluster-mutating target) depend on `on-kind`** or an equivalent guard.
2. **Every `kubectl` in the Makefile passes `--context $(KIND_CONTEXT)`.**
3. **Linter version lives in `.golangci.toml`**, not in CI YAML.
4. **`.PHONY` declares all non-file targets.**
5. **`make test` runs without a kubectl context** — no integration leakage into the unit-test target.
