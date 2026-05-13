---
name: kubernetes-cilium-network-policies
description: "Cilium NetworkPolicies (CNP, CCNP) — endpointSelector, egress rules, DNS egress, serviceAccount selectors. Not for stock Kubernetes NetworkPolicies or unrelated networking."
---

# Cilium Network Policies

Network policies are the silent kind of broken — they fail closed, traffic just stops, and the cause is buried in policy match logic. Selectors that work today break tomorrow when an upstream chart changes its labels. Be deliberate.

## Core Rules

**Prefer ServiceAccount-based selectors over label selectors.** ServiceAccount identity is stable: it's set when the workload is created and rarely renamed. Labels drift — a Helm chart upgrade can rename `app.kubernetes.io/name` from `prometheus` to `kube-prometheus-stack` and your policy silently allows or denies the wrong traffic.

```yaml
# Good — ServiceAccount-based, stable across chart upgrades
endpointSelector:
  matchLabels:
    "io.cilium.k8s.policy.serviceaccount": prometheus
    "io.kubernetes.pod.namespace": monitoring

# Risky — label-based, breaks if upstream renames the label
endpointSelector:
  matchLabels:
    app.kubernetes.io/name: prometheus
```

When you must use labels (e.g., the source isn't your workload), pin to labels you control or upstream labels that are documented as stable.

**Generate DNS egress rules per-host, not via wildcards.** Each egress rule that targets a hostname needs a matching DNS allow rule for that hostname. Wildcards like `*.cluster.local` are tempting but blanket-allow more than you intend, and they obscure which workloads actually need which names.

```yaml
egress:
  - toFQDNs:
      - matchName: postgres.databases.svc.cluster.local
    toPorts:
      - ports:
          - port: "5432"
            protocol: TCP
  - toEndpoints:
      - matchLabels:
          "k8s:io.kubernetes.pod.namespace": kube-system
          "k8s:k8s-app": kube-dns
    toPorts:
      - ports:
          - port: "53"
            protocol: UDP
        rules:
          dns:
            - matchName: postgres.databases.svc.cluster.local
```

**Comment non-obvious decisions.** A reader six months later won't remember why the egress rule applies cluster-wide instead of to a specific workload, or why an exception exists for a particular namespace. Inline comments in the manifest pay back fast.

```yaml
# Reason: bucket sidecars in any namespace need to reach the IAM API.
# Scoping to bucket owners would miss the sidecar identity.
endpointSelector: {}
```

## Patterns to Reach For

**Default-deny posture.** Start with `default-deny-all` policies for ingress and egress in each namespace, then add explicit allows. Trying to denylist after the fact is a losing game.

**Policy per workload, not per cluster.** A single mega-policy covering everything makes diffs unreadable. One CNP per controller/workload, scoped tight.

**`fromEntities: [cluster]` and `toEntities: [world]` for the obvious cases.** Cilium's named entities are easier to read than wide CIDR blocks.

**Separate policies for ingress and egress** when the rule sets are independent. A diff to "allow Prometheus to scrape me" shouldn't have to touch the egress rules.

## Patterns to Avoid

**Don't use `*.cluster.local` or `*.svc.cluster.local` as a DNS allow wildcard.** It permits resolution of every cluster-internal name. Be explicit.

**Don't grant `toEntities: [world]` without a comment** explaining what external service the workload calls.

**Don't rely on label selectors that target `kube-system` components by name.** Those labels change between Kubernetes versions. Pin to ServiceAccount identity (`kube-dns`, `coredns`) when possible.

**Don't author a policy without testing it.** `cilium connectivity test`, or hit the workload from the expected sources and verify denied sources actually fail.

## Mandatory Checks Before Shipping

1. **Selectors target ServiceAccount identity** wherever feasible. Justify any pure-label selector in a comment.
2. **Every `toFQDNs` entry has a matching DNS egress rule** to kube-dns for that name.
3. **No DNS wildcards.** Each egress hostname is listed.
4. **Non-obvious scope choices are commented** (cluster-wide rules, namespace exceptions, broad `endpointSelector: {}`).
5. **Default-deny is in place** for the namespace, with the policy adding allows on top.
6. **Tested.** Either via `cilium connectivity test` or manual smoke from allowed/denied sources.
