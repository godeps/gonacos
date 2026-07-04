package auth

import (
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService()
}

func TestBootstrapAdminSeedsDefaultUser(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	user, err := s.BootstrapAdmin("")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if user.Username != DefaultAdminUser || !user.GlobalAdmin {
		t.Fatalf("admin user: %+v", user)
	}
	if !s.HasGlobalAdmin(DefaultAdminUser) {
		t.Fatalf("global admin role not bound")
	}
	if _, err := s.BootstrapAdmin("pass"); err != ErrAdminExists {
		t.Fatalf("re-bootstrap: %v", err)
	}
}

func TestBootstrapAdminWithCustomPassword(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.BootstrapAdmin("custom123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	result, err := s.Login(DefaultAdminUser, "custom123")
	if err != nil {
		t.Fatalf("login with custom password: %v", err)
	}
	if result.AccessToken == "" || !result.GlobalAdmin {
		t.Fatalf("login result: %+v", result)
	}
}

func TestCreateUserValidation(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreateUser("", "password"); err != ErrMissingUsername {
		t.Fatalf("missing username: %v", err)
	}
	if _, err := s.CreateUser("u", ""); err != ErrMissingPassword {
		t.Fatalf("missing password: %v", err)
	}
	if _, err := s.CreateUser("u", "123"); err != ErrInvalidPasswordFormat {
		t.Fatalf("short password: %v", err)
	}
	if _, err := s.CreateUser("validuser", "password123"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateUser("validuser", "password123"); err != ErrUserExists {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreateUser("alice", "alicepass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Login("alice", "wrongpass"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Login("bob", "anypass"); err != ErrInvalidCredentials {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestUpdateUserRevokesTokens(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreateUser("bob", "bobpass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	result, err := s.Login("bob", "bobpass")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := s.VerifyToken(result.AccessToken); err != nil {
		t.Fatalf("verify before update: %v", err)
	}
	if err := s.UpdateUser("bob", "newpass123"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := s.VerifyToken(result.AccessToken); err != ErrExpiredToken {
		t.Fatalf("verify after update: %v", err)
	}
	if _, err := s.Login("bob", "newpass123"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestDeleteUserRemovesRoleBindings(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreateUser("carol", "carolpass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateRole("ops", "carol"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := s.DeleteUser("carol"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	roles := s.GetRoles("carol")
	if len(roles) != 0 {
		t.Fatalf("roles after delete: %v", roles)
	}
}

func TestCreateDeleteRole(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := s.CreateUser("dave", "davepass"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.CreateRole("ops", "dave"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := s.CreateRole("ops", "dave"); err != ErrRoleExists {
		t.Fatalf("duplicate role: %v", err)
	}
	if err := s.CreateRole("ops", "nobody"); err != ErrUserNotFound {
		t.Fatalf("role for missing user: %v", err)
	}

	if err := s.DeleteRole("ops", "dave"); err != nil {
		t.Fatalf("delete role binding: %v", err)
	}
	if err := s.DeleteRole("ops", ""); err != ErrRoleNotFound {
		t.Fatalf("delete missing role: %v", err)
	}
}

func TestListRolesWithFilters(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := s.CreateUser("eve", "evepass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateRole("dev", "eve"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := s.CreateRole("ops", "eve"); err != nil {
		t.Fatalf("create role 2: %v", err)
	}

	page, err := s.ListRoles(1, 100, "eve", "", "accurate")
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if page.TotalCount != 2 {
		t.Fatalf("count = %d, want 2", page.TotalCount)
	}
	page, err = s.ListRoles(1, 100, "", "ops", "accurate")
	if err != nil {
		t.Fatalf("list by role: %v", err)
	}
	if page.TotalCount != 1 || page.PageItems[0].Username != "eve" {
		t.Fatalf("page = %+v", page)
	}
}

func TestPermissionCRUD(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := s.CreatePermission(AdminRole, "namespace:public", "r"); err != nil {
		t.Fatalf("create permission: %v", err)
	}
	if err := s.CreatePermission(AdminRole, "namespace:public", "r"); err != ErrPermissionExists {
		t.Fatalf("duplicate permission: %v", err)
	}
	exists, _ := s.HasPermission(AdminRole, "namespace:public", "r")
	if !exists {
		t.Fatalf("permission not found")
	}
	if err := s.DeletePermission(AdminRole, "namespace:public", "r"); err != nil {
		t.Fatalf("delete permission: %v", err)
	}
	exists, _ = s.HasPermission(AdminRole, "namespace:public", "r")
	if exists {
		t.Fatalf("permission still exists after delete")
	}
}

func TestPermissionValidation(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if err := s.CreatePermission("", "res", "r"); err != ErrMissingRole {
		t.Fatalf("missing role: %v", err)
	}
	if err := s.CreatePermission("role", "", "r"); err != ErrMissingResource {
		t.Fatalf("missing resource: %v", err)
	}
	if err := s.CreatePermission("role", "res", ""); err != ErrMissingAction {
		t.Fatalf("missing action: %v", err)
	}
	if err := s.CreatePermission("role", "res", "invalid"); err == nil {
		t.Fatalf("invalid action: want error")
	}
	if err := s.CreatePermission("missingrole", "res", "r"); err != ErrRoleNotFound {
		t.Fatalf("missing role: %v", err)
	}
}

func TestAuthorizeAdminBypasses(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	result, _ := s.Login(DefaultAdminUser, "adminpass")
	claims, err := s.VerifyToken(result.AccessToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := s.Authorize(claims, "any:resource", "w"); err != nil {
		t.Fatalf("admin authorize: %v", err)
	}
}

func TestAuthorizeNonAdminRequiresPermission(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := s.CreateUser("frank", "frankpass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateRole("ops", "frank"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := s.CreatePermission("ops", "config:public", "r"); err != nil {
		t.Fatalf("create permission: %v", err)
	}

	result, _ := s.Login("frank", "frankpass")
	claims, _ := s.VerifyToken(result.AccessToken)

	if err := s.Authorize(claims, "config:public", "r"); err != nil {
		t.Fatalf("read permitted: %v", err)
	}
	if err := s.Authorize(claims, "config:public", "w"); err != ErrAccessDenied {
		t.Fatalf("write denied: %v", err)
	}
	if err := s.Authorize(claims, "config:other", "r"); err != ErrAccessDenied {
		t.Fatalf("other resource denied: %v", err)
	}
}

func TestVerifyTokenRejectsTampered(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	result, _ := s.Login(DefaultAdminUser, "adminpass")
	if _, err := s.VerifyToken(result.AccessToken); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if _, err := s.VerifyToken(result.AccessToken + "tamper"); err != ErrInvalidToken {
		t.Fatalf("tampered token: %v", err)
	}
	if _, err := s.VerifyToken("not.a.token"); err != ErrInvalidToken {
		t.Fatalf("malformed token: %v", err)
	}
}

func TestListUsersBlurSearch(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateUser("alice", "alicepass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateUser("bob", "bobpass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	page, _ := s.ListUsers(1, 100, "ali", "blur")
	if page.TotalCount != 1 || page.PageItems[0].Username != "alice" {
		t.Fatalf("blur search: %+v", page)
	}
	page, _ = s.ListUsers(1, 100, "alice", "accurate")
	if page.TotalCount != 1 {
		t.Fatalf("accurate search: %+v", page)
	}
	page, _ = s.ListUsers(1, 100, "nobody", "blur")
	if page.TotalCount != 0 {
		t.Fatalf("no match: %+v", page)
	}
}

func TestSearchUsersAndRoles(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.BootstrapAdmin("adminpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := s.CreateUser("alice", "alicepass"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateRole("developer", "alice"); err != nil {
		t.Fatalf("create role: %v", err)
	}
	names, _ := s.SearchUsers("ali")
	if len(names) != 1 || names[0] != "alice" {
		t.Fatalf("search users: %v", names)
	}
	roles, _ := s.SearchRoles("dev")
	if len(roles) != 1 || roles[0] != "developer" {
		t.Fatalf("search roles: %v", roles)
	}
}
