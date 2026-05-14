---
name: kubernetes-grafana-dashboards
description: "Grafana dashboard JSON and ConfigMaps — datasource UIDs, panel definitions, dashboard provisioning. Not for Prometheus rules/alerts or non-Grafana visualization."
paths:
  - "**/dashboards/**"
  - "**/*dashboard*.json"
  - "**/*dashboard*.yaml"
---

# Grafana Dashboards

Dashboards are configuration, not artifacts of a one-time export. Authoring them with portability in mind means they survive Grafana upgrades, environment moves, and being installed alongside other dashboards.

## Core Rules

**Set `"id": null` in dashboard JSON templates.** A hardcoded numeric `id` collides with whatever exists at that ID in the target Grafana instance. `null` lets Grafana assign one on import.

```json
{
  "id": null,
  "uid": "happi-bucket-overview",
  "title": "Happi Bucket Overview",
  ...
}
```

Set a stable `uid` instead — that's what URLs and provisioning configs reference.

**Parameterize datasource UIDs with template variables.** A dashboard exported with a hardcoded `"datasource": {"uid": "PBFA97CFB590B2093"}` is bound to one Grafana instance. Use `${datasource}` and define a template variable so the same JSON works in dev, staging, prod.

```json
{
  "templating": {
    "list": [
      {
        "name": "datasource",
        "type": "datasource",
        "query": "prometheus"
      }
    ]
  },
  "panels": [
    {
      "datasource": { "uid": "${datasource}" },
      ...
    }
  ]
}
```

**Prefer metric queries over synchronous REST calls.** When a panel needs data from a system that exposes both Prometheus metrics and a live API (e.g., opencost), use the metrics. Metric queries are cached, scoped to a time range, and survive the source being temporarily unavailable. REST queries from a panel hit the source on every dashboard load and break when the source is slow.

## Patterns to Reach For

**Stable `uid`s, descriptive `title`s.** UID is the dashboard's permanent identifier; pick something readable (`happi-bucket-overview`) so URLs make sense.

**Template variables for environment, namespace, workload.** Lets one dashboard serve every environment instead of duplicating per-env.

**Folder organization via provisioning config**, not by mutating dashboard JSON. Folder assignment lives in the Grafana provisioning YAML.

**Version control as YAML-wrapped ConfigMaps**, with the dashboard JSON in a sub-key. Easier to review than diffing a 5000-line export.

## Patterns to Avoid

**Don't commit dashboards exported via the Grafana UI without normalizing.** UI exports include `id`, hardcoded datasource UIDs, `__inputs`, `__requires`, and other instance-specific noise. Strip them before committing.

**Don't hardcode environment names in panel titles or queries.** Use template variables. `Bucket usage in $env` beats one dashboard per env.

**Don't wire dashboards directly to live external APIs** when metrics are available. The dashboard becomes a load source on the API and an outage amplifier.

## Mandatory Checks Before Shipping

1. **`id` is `null`** in committed dashboard JSON.
2. **`uid` is set and stable** — don't change it once published.
3. **All datasource references use `${datasource}`** (or another template variable), not a hardcoded UID.
4. **No `__inputs` / `__requires` / `__elements`** blocks left over from UI export.
5. **Metric-based panels preferred** over synchronous REST calls where both exist.
6. **Dashboard imports cleanly** in a fresh Grafana instance via provisioning.
