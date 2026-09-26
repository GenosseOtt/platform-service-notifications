# Notification opt-out RBAC

The two opt-out CRDs (`NotificationOptOut`, `UserNotificationOptOut`) live on the **onboarding**
cluster in project/workspace namespaces. Access is granted via openmcp's
[`ProjectWorkspaceConfig`](https://docs.open-control-plane.io/openmcp-operator/project-workspace-config/)
`additionalPermissions` map — the only extension point available, since openmcp does not support
ClusterRole label aggregation.

## Who can do what

| CRD | Verb | Role |
|-----|------|------|
| `NotificationOptOut` | `get`, `list`, `watch` | `project-admin`, `project-view`, `workspace-admin`, `workspace-view` |
| `NotificationOptOut` | `create`, `update`, `patch`, `delete` | `project-admin`, `workspace-admin` |
| `UserNotificationOptOut` | `get`, `list`, `watch` | `project-admin`, `project-view`, `workspace-admin`, `workspace-view` |
| `UserNotificationOptOut` | `create`, `update`, `patch`, `delete` | `project-admin`, `project-view`, `workspace-admin`, `workspace-view` |

**`NotificationOptOut`** (resource-wide mute) is admin-only because it affects all users in the
namespace.

**`UserNotificationOptOut`** (per-user mute) is creatable by any member, including viewers.
There is **no admission webhook** enforcing "only mute yourself" — that is a soft convention.
The worst-case abuse is a member silencing a colleague's email for a namespace they both have
access to (annoyance, not a privilege escalation).

## ProjectWorkspaceConfig sample

Apply this `ProjectWorkspaceConfig` once per platform deployment. It adds the notification
opt-out verbs to all project and workspace member ClusterRoles via openmcp's
`additionalPermissions` extension.

```yaml
apiVersion: openmcp.cloud/v1alpha1
kind: ProjectWorkspaceConfig
metadata:
  name: notifications-optout
  namespace: <provider-system-namespace>     # e.g. openmcp-system
spec:
  additionalPermissions:
    # ── Project roles ────────────────────────────────────────────────────────
    project-admin:
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["notificationoptouts", "usernotificationoptouts"]
        verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
    project-view:
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["notificationoptouts"]
        verbs: ["get", "list", "watch"]
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["usernotificationoptouts"]
        verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
    # ── Workspace roles ───────────────────────────────────────────────────────
    workspace-admin:
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["notificationoptouts", "usernotificationoptouts"]
        verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
    workspace-view:
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["notificationoptouts"]
        verbs: ["get", "list", "watch"]
      - apiGroups: ["notifications.platform.open-control-plane.io"]
        resources: ["usernotificationoptouts"]
        verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

## Namespace scoping

openmcp binds member ClusterRoles in the relevant namespace only, so:

- A `project-admin` in `project-poc` can create opt-outs in `project-poc` only.
- A `workspace-admin` in `project-poc--ws-dev` can create opt-outs in `project-poc--ws-dev` only.

This namespace scope is what enforces authorization: a `NotificationOptOut` placed in
`project-poc` by a project admin mutes at the project level; one placed in
`project-poc--ws-dev` by a workspace admin mutes at the workspace level.

## Known limitations

1. **No ControlPlane-level RBAC.** openmcp has no CP-level member role today. A workspace admin
   must create the `NotificationOptOut{target:{kind:ControlPlane, name:...}}` in the workspace
   namespace on behalf of the CP owner.

2. **No cluster-wide user self-mute.** A user who wants to mute a project's notifications must
   create one `UserNotificationOptOut{target:{kind:Project,...}}` in the project namespace. This
   cascades to all workspaces and CPs under that project.

3. **Soft subject enforcement.** `UserNotificationOptOut.spec.subject` is not validated by an
   admission webhook (our pod runs on the platform cluster and cannot serve webhooks to the
   onboarding API). Members can technically mute teammates in the same namespace; this is an
   annoyance risk, not a security escalation.
