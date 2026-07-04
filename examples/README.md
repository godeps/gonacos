# gonacos examples

End-to-end demos that exercise the gonacos server against the upstream
`nacos-sdk-go/v2` client (gRPC) and the v3 HTTP API. Each example runs a
complete lifecycle — create, read, subscribe, update, delete — and prints
`ALL STEPS PASSED` on success.

## Prerequisites

1. **gonacos server running on `127.0.0.1:8848`** — from the repo root:

   ```sh
   GOWORK=off go build -o /tmp/gonacos-test ./cmd/gonacos
   /tmp/gonacos-test serve :8848
   ```

2. **admin user bootstrapped** — in another terminal:

   ```sh
   curl -X POST 'http://127.0.0.1:8848/v3/auth/user/admin' -d 'password=nacos'
   ```

## Run

All examples use `GOWORK=off` because gonacos is not in the workspace's
`go.work`. Run from the repo root:

```sh
GOWORK=off go run ./examples/config
GOWORK=off go run ./examples/naming
GOWORK=off go run ./examples/namespace
GOWORK=off go run ./examples/auth
```

## What each example covers

### `examples/config` — config publish / get / listen / delete (gRPC)

Uses the nacos-sdk-go **config client**. Publishes a YAML config, reads it
back, registers an `OnChange` listener, publishes a new version (verifying
the listener fires), cancels the listener, deletes the config, and verifies
the read-after-delete returns an error.

### `examples/naming` — service register / discover / subscribe / deregister (gRPC)

Uses the nacos-sdk-go **naming client**. Registers an instance, subscribes
(receives the initial push), discovers, registers a second instance
(verifying the subscribe push carries 2 instances), deregisters the second
(verifying the push drops back to 1), unsubscribes, and deregisters the
first.

The example subscribes **before** calling `SelectInstances` — the SDK's
internal cache otherwise short-circuits the explicit `Subscribe` call and
the initial push callback never fires.

### `examples/namespace` — namespace CRUD (HTTP)

Uses the v3 HTTP API directly (no SDK). Lists namespaces (expects the
built-in `public`), creates a new namespace, gets its detail, lists again
(expects the new namespace), deletes it, lists again (expects it gone).

### `examples/auth` — auth + RBAC CRUD (HTTP)

Uses the v3 HTTP API directly. Logs in as admin, creates a non-admin user,
lists users, logs in as the new user, creates a role for it, lists roles,
creates a permission, lists permissions, checks `hasPermission`, then
cleans up (delete permission / role / user) and verifies the user is gone.

## Notes

- The naming example relies on server push via the gRPC BiRequestStream.
  gonacos includes a strictly-monotonic `lastRefTime` in every
  `serviceInfo` response so the SDK's `ProcessService` out-of-date check
  (`oldRefTime >= newRefTime`) does not drop back-to-back pushes.
- The naming example's `Subscribe` call must precede any `SelectInstances`
  call. `SelectInstances` populates the SDK's internal `ServiceInfoMap`
  cache; a subsequent `Subscribe` finds the entry cached and skips the RPC,
  so the callback registration never pairs with a push.
- The `requestId` field in `NotifySubscriberRequest` / `ConfigChangeNotifyRequest`
  push frames is required — without it the SDK's `json.Unmarshal` leaves
  the embedded `*Request` nil and the next `PutAllHeaders` call panics.
