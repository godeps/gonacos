// Package auth implements the default Nacos v3 auth plugin: user, role,
// permission CRUD, admin bootstrap, and JWT-like token issuance.
//
// The Service type owns an in-memory user/role/permission registry. Tokens are
// signed with HMAC-SHA256 and verified on each protected request. The default
// admin user "nacos" with password "nacos" is seeded on first construction
// unless CreateAdmin is called explicitly.
package auth
