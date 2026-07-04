// Package namespace implements the Nacos v3 namespace service: CRUD over
// namespaces with the default public namespace seeded at startup.
//
// Namespaces partition configuration and naming data so multiple tenants can
// share a single server. The Service type owns an in-memory registry; the
// store coordinator snapshots and restores it alongside the other domain
// services.
package namespace
