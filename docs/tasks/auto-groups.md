# Auto Groups (OpenShift)

With `--auto-groups`, kube-oidc-proxy uses OIDC only to authenticate the user.
The user's groups are resolved from OpenShift
[Group](https://docs.openshift.com/container-platform/latest/rest_api/user_and_group_apis/group-user-openshift-io-v1.html)
objects (`user.openshift.io/v1`) instead of the token.

## How it works

1. The OIDC token is validated, and the username is built from
   `--oidc-username-claim` and `--oidc-username-prefix`.
2. The proxy looks up every Group whose `users` list contains that **final
   username**. The match is exact and case sensitive, so with
   `--oidc-username-prefix=oidc:` the Group must list `oidc:alice`.
3. Those group names, used as is, **replace** the token groups.
   `--oidc-groups-claim` and `--oidc-groups-prefix` are ignored.
4. The [identity filter](./identity-filter.md) (group allow/deny regex and
   `--required-group`) runs on the resolved groups. The request is then
   impersonated with them (plus `system:authenticated`).

```
OIDC token ─▶ username "alice"
                    │
OpenShift Groups ───┤  k8s-admins.users = [alice, bob]
 (watch cache)      │  k8s-users.users  = [alice, carol]
                    ▼
Impersonate-User: alice
Impersonate-Group: k8s-admins, k8s-users, system:authenticated
```

- **Cache**: all Groups are watched into memory. Lookups never hit the API
  server, and changes to Groups apply within seconds.
- **Fail-closed**: the proxy refuses to start if the cluster does not serve
  `user.openshift.io/v1` groups, or if the cache is not synced within
  `--auto-groups-sync-timeout` (default `1m`), for example because of missing
  RBAC. If the watch breaks later, the last known state is kept and the watch
  retries.
- **Impersonation targets** (`kubectl --as=bob`) do **not** get bob's OpenShift
  groups, same as the API server. Request groups explicitly with `--as-group`,
  which needs the `impersonate` permission on groups.
- **Not affected**: [token passthrough](./token-passthrough.md) requests. With
  [no impersonation](./no-impersonation.md), resolved groups are only used by
  the identity filter.

## RBAC

The proxy service account needs:

```yaml
- apiGroups: ["user.openshift.io"]
  resources: ["groups"]
  verbs: ["list", "watch"]
```

The Helm chart adds this rule when `autoGroups.enabled=true`.

## Example

```bash
kube-oidc-proxy \
  --oidc-issuer-url=https://sso.example.com/realms/k8s \
  --oidc-client-id=kube \
  --oidc-username-claim=preferred_username \
  --auto-groups \
  --required-group=k8s-users \
  ...
```

> Without `--oidc-username-prefix`, this proxy uses the claim value as is (no
> issuer URL prefix). Note that `--oidc-username-prefix=-` literally prepends
> `-`.

Helm chart:

```yaml
oidc:
  usernameClaim: preferred_username
autoGroups:
  enabled: true
identityFilter:
  requiredGroups: ['k8s-users']   # must be a member of this OpenShift group
```

> The chart's default identity filter only accepts groups made of ASCII
> letters, digits and `-`. OpenShift groups with other characters (e.g. `.`,
> `_`, groups synced from LDAP) reject the request. Adjust
> `identityFilter.allowedGroupRegex` if needed.
