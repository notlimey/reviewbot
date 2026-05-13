---
name: kubernetes-kustomize
description: "Kustomize bases and overlays — kustomization.yaml, patchesStrategicMerge, remote refs, env-specific manifests. Not for Helm charts or non-Kubernetes config."
---

# Kustomize & Deployment Config

Kustomize works when bases stay generic and overlays carry the differences. Most issues come from leaking environment-specific values into the base, or from committing build artifacts that should be regenerated.

## Core Rules

**Environment-specific config belongs in overlays, not the base.** Cluster names, hostnames, replica counts, namespace names, image tags — all overlay material. Bases describe the shape of the deployment; overlays plug in the values for `dev`, `staging`, `prod`, `local`.

```
kustomize/
  base/
    deployment.yaml         # generic, no environment values
    kustomization.yaml
  overlays/
    local/                  # kind cluster
      kustomization.yaml
      patches/
    dev/
    prod/
```

**Never commit `kustomize build` output.** Generated manifests rot the moment the base changes. They belong in `.gitignore`.

**Use broad glob patterns in `.gitignore`** so new modules inherit the rule. `kustomize/local/**/charts/` survives adding a new component; `kustomize/local/operator/charts/` does not.

```gitignore
# Good — future-proof
kustomize/**/charts/
kustomize/**/built/

# Bad — needs editing every time someone adds a module
kustomize/local/operator/built/
```

**Prefer kustomize remote refs over vendoring CRDs.** When you depend on an upstream CRD bundle, reference it by Git URL pinned to a tag — don't copy the YAML into the repo. Updates become a one-line ref bump instead of a diff review.

```yaml
# Good
resources:
  - git@github.com:cloudnative-pg/plugin-barman-cloud/config/crd?ref=v0.10.0

# Bad — entire CRD inlined and slowly drifting from upstream
resources:
  - vendored/plugin-barman-cloud-crd.yaml
```

**Clean up fully when removing config.** A half-removed feature leaves orphan ConfigMaps, unused overlay patches, and dangling `resources:` entries. Either fully revert or fully remove.

**Verbose env var mapping is acceptable when key names don't align.** If an upstream secret uses `DATABASE_URL` but your app reads `PG_DSN`, an explicit `env:` block with `valueFrom.secretKeyRef` is clearer than reshaping the secret. Don't force `envFrom` if the keys don't match — the indirection costs more than the verbosity.

## Patterns to Reach For

**`namePrefix` / `nameSuffix` for environment isolation.** Lets multiple overlays coexist in the same cluster without collision.

**`commonLabels` for fleet-wide tags.** `app.kubernetes.io/name`, `team`, `env` — set once at the overlay root.

**`replacements` for cross-resource values.** When a field in resource A needs to match a field in resource B (e.g., a Service name into an env var), use `replacements` rather than maintaining the value in two places.

**Component pattern for opt-in features.** `components/` lets an overlay turn on cross-cutting concerns (e.g., metrics scraping, network policies) without forking the base.

## Patterns to Avoid

**Don't put production hostnames or secrets in the base.** "Just for now" turns into "still there in two years."

**Don't use `kustomize build > out.yaml` and commit `out.yaml`.** That defeats the point of kustomize.

**Don't mix Helm and kustomize haphazardly.** If you wrap Helm charts via `helmCharts:`, document the choice and stick to it. Random patches over Helm output make upgrades miserable.

**Don't reference local paths from one overlay into another overlay.** Overlays should reference the base or shared `components/`, not each other.

## Mandatory Checks Before Shipping

1. **No environment-specific values in `base/`.** Grep for hostnames, replica counts, image tags.
2. **No generated `kustomize build` output committed.** `.gitignore` covers it with broad globs.
3. **Upstream CRDs are referenced via remote ref**, not vendored, unless there's a documented reason.
4. **Removing a feature removed *all* of its config** — base entries, overlay patches, secrets, ConfigMaps.
5. **`kustomize build overlays/<env>` succeeds** for every env you ship.
