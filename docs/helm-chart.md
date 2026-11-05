# Image and Helm chart (nobidev fork)

`.github/workflows/release.yaml` runs on every push to `master` and on pull
requests:

| Job | PR | `master` |
|---|---|---|
| `test` | `go test ./pkg/... ./cmd/...` | same |
| `lint` | lint/template/package the chart | same |
| `image` | build `linux/amd64` image, no push | build + push `linux/amd64,linux/arm64` |
| `publish` | — | push chart to GHCR (OCI) and gh-pages |

The inherited upstream `build.yaml` is skipped on this fork (it needs upstream
secrets).

## Image

```
ghcr.io/nobidev/kube-oidc-proxy:<appVersion>-<sha7>   # immutable, used by the chart
ghcr.io/nobidev/kube-oidc-proxy:<appVersion>          # overwritten on every build
ghcr.io/nobidev/kube-oidc-proxy:latest                # overwritten on every build
```

## Install the chart

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
| `appVersion` | `PROJ_VERSION` in `.github/workflows/build.yaml` |
| chart `version` | same as `appVersion` (override with `workflow_dispatch` input `chart_version`) |
| `image.repository` | repo variable `HELM_IMAGE_REPOSITORY`, default `ghcr.io/nobidev/kube-oidc-proxy` |
| `image.tag` | `<appVersion>-<sha7>` of the commit the chart was built from |

In git, `values.yaml` has `image.tag: ""`, which falls back to the chart
`appVersion` (useful for installing from a checkout).

Every run **rebuilds and overwrites** the chart for the current version (same
as upstream re-pushing image tags). Because the packaged chart pins the
immutable `<appVersion>-<sha7>` image tag, an upgrade to the overwritten chart
always rolls out the matching image, even with `imagePullPolicy: IfNotPresent`.
Clients must refresh to pick up an overwritten chart:

```bash
helm repo update                       # classic repo: refresh index (new digest)
helm pull oci://ghcr.io/nobidev/charts/kube-oidc-proxy --version 1.0.13   # OCI: always fetches
```

GitOps tools that cache by version (Argo CD, Flux) will not redeploy on an
overwrite by themselves; trigger a hard refresh / reconcile.

## Identity filter defaults

The chart enables the [identity filter](./tasks/identity-filter.md) by default:
usernames and groups may only contain ASCII letters, digits and `-`. This
rejects `system:*` users/groups, service accounts, e-mail usernames and any
`usernamePrefix`/`groupsPrefix` containing `:`. Set
`identityFilter.allowedUsernameRegex` / `allowedGroupRegex` to `[]` to disable.

## One-time setup

1. **GHCR visibility**: the `kube-oidc-proxy` image and `charts/kube-oidc-proxy`
   chart packages are created **private** by default. Make them public in
   *Package settings → Change visibility*, or configure an image pull secret
   (`imagePullSecrets`) / `helm registry login`.
2. **GitHub Pages**: enable Pages in *Settings → Pages → Deploy from a branch →
   `gh-pages` / `(root)`*. Set repo variable `HELM_PAGES_ENABLED=false` to
   skip Pages publishing entirely.
