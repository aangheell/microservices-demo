# Add user accounts (userservice)

This component adds sign-in and registration to the frontend. It deploys:

- `userservice`: a gRPC service (Python) that stores accounts and issues JWTs
- `postgres-user`: a single-replica Postgres StatefulSet for the accounts
- a patch for the `frontend` that sets `USER_SERVICE_ADDR` and `JWT_SECRET`

Without this component, the frontend runs as before and shows no Sign In button.

## 1. Create the Secret

The credentials are not stored in git. `userservice`, `postgres-user` and
`frontend` all read them from a Secret named `userservice-secret`, with the keys
`db-password` and `jwt-secret` (at least 32 bytes).

Create it once per namespace with random values:

```sh
kustomize/components/user-service/create-secret.sh [namespace]
```

The script does nothing if the Secret already exists, so you can run it on
every deploy. To use your own secret manager instead (External Secrets, Sealed
Secrets, etc.), produce a Secret with the same name and keys.

## 2. Enable the component

From the `kustomize/` folder:

```sh
kustomize edit add component components/user-service
```

Or add it to `kustomization.yaml` by hand, **before** the `container-images-*`
components:

```yaml
components:
- components/user-service
```

`userservice` is not published to the upstream Online Boutique registry. Push
it to your own registry and point the
[container-images-registry](../container-images-registry) and
[container-images-tag](../container-images-tag) components at it.

If you use [network-policies](../network-policies), its policies already cover
`userservice` and `postgres-user`.

## Rotating the JWT secret

Replace `jwt-secret` in the Secret, then restart `userservice` and `frontend`.
All existing sessions are signed out.

## Upgrading from the first version of this service

The first version committed `userservice-secret` (with `db-password:
shoppassword`) to git and stored Postgres data in a standalone PVC named
`postgres-user-pvc`. `create-secret.sh` prints a warning if it finds those
credentials.

The StatefulSet now uses `volumeClaimTemplates` and a `PGDATA` subdirectory.
Kubernetes cannot change `volumeClaimTemplates` on an existing StatefulSet, so
upgrading needs a one-time reset. **This deletes all existing user accounts.**

```sh
kubectl delete statefulset postgres-user
kubectl delete pvc postgres-user-pvc
kubectl delete secret userservice-secret
kustomize/components/user-service/create-secret.sh
# then deploy as usual (skaffold run / kubectl apply -k)
```
