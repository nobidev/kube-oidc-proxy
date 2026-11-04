# Helm chart (nobidev fork)

The chart in `deploy/charts/kube-oidc-proxy` is built and published by
`.github/workflows/helm.yaml` on every push to `master` that touches the chart
or the workflows (including upstream syncs that bump `PROJ_VERSION` in
`build.yaml`). Pull requests only lint/template/package the chart.

## Install

OCI (GHCR):

```bash
helm install kube-oidc-proxy oci://ghcr.io/nobidev/charts/kube-oidc-proxy --version 1.0.13
```

Classic Helm repository (GitHub Pages):

```bash
helm repo add nobidev https://nobidev.github.io/kube-oidc-proxy
helm install kube-oidc-proxy nobidev/kube-oidc-proxy --version 1.0.13
```

## Versioning

| Field | Source |
|---|---|
| `appVersion` / `image.tag` | `PROJ_VERSION` in `.github/workflows/build.yaml` |
| chart `version` | same as `appVersion` (override with `workflow_dispatch` input `chart_version`) |
| `image.repository` | repo variable `HELM_IMAGE_REPOSITORY`, default `docker.io/tremolosecurity/kube-oidc-proxy` |

The chart sources are not modified in git; these values are injected at
package time so syncing with upstream stays conflict-free.

Every run **rebuilds and overwrites** the chart for the current version (same
as upstream re-pushing image tags), so chart-only changes are published under
the same version on the next push to `master`. Clients must refresh to pick
them up, e.g.:

```bash
helm repo update                       # classic repo: refresh index (new digest)
helm pull oci://ghcr.io/nobidev/charts/kube-oidc-proxy --version 1.0.13   # OCI: always fetches
```

GitOps tools that cache by version (Argo CD, Flux) will not redeploy on an
overwrite by themselves; trigger a hard refresh / reconcile.

## One-time setup

1. **GitHub Pages**: after the first successful run creates the `gh-pages`
   branch, enable Pages in *Settings → Pages → Deploy from a branch →
   `gh-pages` / `(root)`*. Set repo variable `HELM_PAGES_ENABLED=false` to
   skip Pages publishing entirely.
2. **GHCR visibility**: the `charts/kube-oidc-proxy` package is created private
   by default. Make it public in *Package settings → Change visibility* if
   anonymous `helm pull` is needed.
3. **Actions on the fork**: make sure Actions are enabled for this fork
   (*Actions* tab). Note that the inherited `build.yaml` needs upstream
   secrets (`OU_REG_USER`, `OU_CONTAINER_DEST`, ...) and will fail on the fork;
   the chart uses the upstream-published image instead.
