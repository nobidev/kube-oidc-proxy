# Identity Filter

The identity filter restricts which OIDC users and groups may use the proxy,
independently of Kubernetes RBAC. Rejected requests get `403 Forbidden` and are
never forwarded to the API server. The reason is logged by the proxy (log
level `-v=2`) but not returned to the client.

## Flags

| Flag | Applies to | Rule |
|---|---|---|
| `--allowed-username-regex` | OIDC user, impersonated user | If set, the username must match at least one expression |
| `--denied-username-regex` | OIDC user, impersonated user | The username must match none. Takes precedence over allow |
| `--allowed-group-regex` | OIDC groups, impersonated groups | If set, **every** group must match at least one expression |
| `--denied-group-regex` | OIDC groups, impersonated groups | **No** group may match. Takes precedence over allow |
| `--required-group` | OIDC user only | The user must be a member of **all** listed groups (exact names) |

All flags can be repeated. Values are not split on commas.

## Semantics

- **Anchored expressions**: every expression must match the whole value
  (Go [RE2](https://github.com/google/re2/wiki/Syntax) syntax, wrapped as
  `^(?:expr)$`). `admins` does not match `not-admins`, so write `.*admins`
  if a partial match is wanted.
- **Matched after prefixing**: values are matched after
  `--oidc-username-prefix` / `--oidc-groups-prefix` are applied, i.e. against
  the names Kubernetes sees.
- **Groups are rejected, not dropped**: a single group that is not allowed or
  is denied rejects the whole request. If your IdP sends many unrelated groups,
  use deny lists, or an allow list broad enough to cover them.
- **Required groups (AND)** are checked against the OIDC groups after the group
  filter has passed. Required groups do not apply to impersonation targets.
- **Impersonation**: `Impersonate-User` / `Impersonate-Group` targets are
  checked against the username/group allow/deny lists in addition to the RBAC
  `impersonate` check, so `kubectl --as` / `--as-group` cannot bypass the
  filter. The proxy-added `system:authenticated` group is not filtered.
- **Not filtered**: requests authenticated with
  [token passthrough](./token-passthrough.md) (non-OIDC tokens validated by
  TokenReview) are forwarded as is, and the API server authorizes them itself.

## Example

Only users from `example.com`, never `system:*` names or `system:masters`, and
the user must be in both `k8s-users` and `mfa`:

```bash
kube-oidc-proxy \
  --oidc-username-claim=email --oidc-username-prefix=oidc: \
  --oidc-groups-claim=groups --oidc-groups-prefix=oidc: \
  --allowed-username-regex='oidc:.+@example\.com' \
  --denied-username-regex='system:.*' \
  --denied-group-regex='system:masters' \
  --required-group=oidc:k8s-users \
  --required-group=oidc:mfa \
  ...
```

Helm chart (the chart ships `allowedUsernameRegex` / `allowedGroupRegex` set to
`['[A-Za-z0-9-]+']` by default, i.e. only ASCII letters, digits and `-`; replace
or set them to `[]` to disable):

```yaml
identityFilter:
  allowedUsernameRegex: ['oidc:.+@example\.com']
  deniedUsernameRegex: ['system:.*']
  deniedGroupRegex: ['system:masters']
  requiredGroups: ['oidc:k8s-users', 'oidc:mfa']
```

> Kubernetes expands `$(VAR)` in container args, so avoid the sequence `$(`
> in expressions when using the chart. Anchors are added automatically, so
> `$` is rarely needed.
