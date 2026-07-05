package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultAdminUser is the Nacos default admin username.
	DefaultAdminUser = "nacos"
	// DefaultAdminPassword is the Nacos default admin password.
	DefaultAdminPassword = "nacos"
	// AdminRole is the Nacos global admin role name.
	AdminRole = "ROLE_ADMIN"
	// DefaultTokenTTL is the default token lifetime in seconds.
	DefaultTokenTTL = 18000 // 5 hours, matches Nacos default
	// TokenPrefix is the Authorization header value prefix.
	TokenPrefix = "Bearer "
	// AuthorizationHeader is the header that carries the access token.
	AuthorizationHeader = "Authorization"
)

var (
	ErrMissingUsername       = errors.New("username is required")
	ErrMissingPassword       = errors.New("password is required")
	ErrMissingRole           = errors.New("role is required")
	ErrMissingResource       = errors.New("resource is required")
	ErrMissingAction         = errors.New("action is required")
	ErrUserExists            = errors.New("user already exists")
	ErrUserNotFound          = errors.New("user not found")
	ErrRoleExists            = errors.New("role already exists")
	ErrRoleNotFound          = errors.New("role not found")
	ErrPermissionExists      = errors.New("permission already exists")
	ErrPermissionNotFound    = errors.New("permission not found")
	ErrInvalidCredentials    = errors.New("invalid username or password")
	ErrInvalidToken          = errors.New("invalid token")
	ErrExpiredToken          = errors.New("token expired")
	ErrAdminExists           = errors.New("admin user already exists")
	ErrAccessDenied          = errors.New("access denied")
	ErrInvalidUsernameFormat = errors.New("invalid username format")
	ErrInvalidPasswordFormat = errors.New("invalid password format")
)

// User is the Nacos-compatible user representation.
type User struct {
	Username    string `json:"username"`
	Password    string `json:"-"`
	Salt        string `json:"-"`
	Enabled     bool   `json:"enabled"`
	GlobalAdmin bool   `json:"globalAdmin"`
}

// Role is the Nacos-compatible role representation.
type Role struct {
	Role     string `json:"role"`
	Username string `json:"username"`
}

// Permission is the Nacos-compatible permission representation.
type Permission struct {
	Role     string `json:"role"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// UserPage is the Nacos-compatible paginated user list.
type UserPage struct {
	TotalCount     int    `json:"totalCount"`
	PageNumber     int    `json:"pageNumber"`
	PagesAvailable int    `json:"pagesAvailable"`
	PageItems      []User `json:"pageItems"`
}

// RolePage is the Nacos-compatible paginated role list.
type RolePage struct {
	TotalCount     int    `json:"totalCount"`
	PageNumber     int    `json:"pageNumber"`
	PagesAvailable int    `json:"pagesAvailable"`
	PageItems      []Role `json:"pageItems"`
}

// PermissionPage is the Nacos-compatible paginated permission list.
type PermissionPage struct {
	TotalCount     int          `json:"totalCount"`
	PageNumber     int          `json:"pageNumber"`
	PagesAvailable int          `json:"pagesAvailable"`
	PageItems      []Permission `json:"pageItems"`
}

// Service owns the in-memory auth registry and token signer.
type Service struct {
	mu          sync.RWMutex
	users       map[string]*User
	roles       map[string][]string        // role -> []username
	userRoles   map[string]map[string]bool // username -> set(role)
	permissions map[string][]Permission    // role -> []permission
	tokens      *tokenManager
}

// NewService creates an auth Service with a random signing secret. The default
// admin user is not seeded here; call BootstrapAdmin explicitly. Panics if the
// system CSPRNG is unavailable, which is a fatal initialization error.
func NewService() *Service {
	secret, err := randomSecret()
	if err != nil {
		panic("auth: cannot read system CSPRNG: " + err.Error())
	}
	return NewServiceWithSecret(secret)
}

// NewServiceWithSecret creates an auth Service signed with the provided secret.
// Use this when running multiple nodes that must verify each other's tokens —
// all nodes in a cluster must share the same secret. An empty secret produces
// a Service whose tokens are unsigned and rejected on verify, which is useful
// only for tests that disable auth.
func NewServiceWithSecret(secret string) *Service {
	return &Service{
		users:       map[string]*User{},
		roles:       map[string][]string{},
		userRoles:   map[string]map[string]bool{},
		permissions: map[string][]Permission{},
		tokens:      newTokenManager(secret),
	}
}

// BootstrapAdmin seeds the default admin user if no admin exists yet. Returns
// ErrAdminExists if an admin user already exists.
func (s *Service) BootstrapAdmin(password string) (User, error) {
	if password == "" {
		password = DefaultAdminPassword
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.GlobalAdmin {
			return User{}, ErrAdminExists
		}
	}
	if _, ok := s.users[DefaultAdminUser]; ok {
		return User{}, ErrAdminExists
	}
	user, err := createUserLocked(DefaultAdminUser, password, true)
	if err != nil {
		return User{}, err
	}
	s.users[DefaultAdminUser] = user
	s.roles[AdminRole] = []string{DefaultAdminUser}
	s.userRoles[DefaultAdminUser] = map[string]bool{AdminRole: true}
	return *user, nil
}

// CreateUser registers a new non-admin user. Returns ErrUserExists if the
// username is taken.
func (s *Service) CreateUser(username, password string) (User, error) {
	if err := validateCredentials(username, password); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[username]; ok {
		return User{}, ErrUserExists
	}
	user, err := createUserLocked(username, password, false)
	if err != nil {
		return User{}, err
	}
	s.users[username] = user
	s.userRoles[username] = map[string]bool{}
	return *user, nil
}

// DeleteUser removes a user and revokes all role bindings and tokens.
func (s *Service) DeleteUser(username string) error {
	if username = strings.TrimSpace(username); username == "" {
		return ErrMissingUsername
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[username]; !ok {
		return ErrUserNotFound
	}
	delete(s.users, username)
	for role, members := range s.roles {
		filtered := members[:0]
		for _, name := range members {
			if name != username {
				filtered = append(filtered, name)
			}
		}
		s.roles[role] = filtered
	}
	delete(s.userRoles, username)
	s.tokens.revokeUser(username)
	return nil
}

// UpdateUser changes the user's password. Tokens issued for the user are
// revoked so the user must re-authenticate.
func (s *Service) UpdateUser(username, newPassword string) error {
	if err := validateCredentials(username, newPassword); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[username]
	if !ok {
		return ErrUserNotFound
	}
	salt, hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	user.Salt = salt
	user.Password = hash
	s.tokens.revokeUser(username)
	return nil
}

// ListUsers returns a paginated user list. The search flag toggles blur vs
// exact matching against the username substring.
func (s *Service) ListUsers(pageNo, pageSize int, username, search string) (UserPage, error) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []*User
	for _, u := range s.users {
		if username == "" {
			matches = append(matches, u)
			continue
		}
		if strings.EqualFold(search, "blur") {
			if strings.Contains(strings.ToLower(u.Username), strings.ToLower(username)) {
				matches = append(matches, u)
			}
		} else if u.Username == username {
			matches = append(matches, u)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Username < matches[j].Username })

	total := len(matches)
	pages := (total + pageSize - 1) / pageSize
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]User, 0, end-start)
	for _, u := range matches[start:end] {
		items = append(items, *u)
	}
	return UserPage{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      items,
	}, nil
}

// SearchUsers returns usernames matching the substring.
func (s *Service) SearchUsers(prefix string) ([]string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for name := range s.users {
		if prefix == "" || strings.Contains(strings.ToLower(name), prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CreateRole binds a role to a user. The role is created if it does not exist.
func (s *Service) CreateRole(role, username string) error {
	if role = strings.TrimSpace(role); role == "" {
		return ErrMissingRole
	}
	if username = strings.TrimSpace(username); username == "" {
		return ErrMissingUsername
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[username]; !ok {
		return ErrUserNotFound
	}
	if s.userRoles[username] == nil {
		s.userRoles[username] = map[string]bool{}
	}
	if s.userRoles[username][role] {
		return ErrRoleExists
	}
	s.userRoles[username][role] = true
	s.roles[role] = append(s.roles[role], username)
	return nil
}

// DeleteRole removes a role binding. If username is empty the entire role is
// removed.
func (s *Service) DeleteRole(role, username string) error {
	if role = strings.TrimSpace(role); role == "" {
		return ErrMissingRole
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	members, ok := s.roles[role]
	if !ok {
		return ErrRoleNotFound
	}
	if username == "" {
		delete(s.roles, role)
		for _, name := range members {
			delete(s.userRoles[name], role)
		}
		return nil
	}
	if _, ok := s.userRoles[username]; !ok || !s.userRoles[username][role] {
		return ErrRoleNotFound
	}
	delete(s.userRoles[username], role)
	filtered := members[:0]
	for _, name := range members {
		if name != username {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		delete(s.roles, role)
	} else {
		s.roles[role] = filtered
	}
	return nil
}

// ListRoles returns a paginated role list filtered by username and/or role.
func (s *Service) ListRoles(pageNo, pageSize int, username, role, search string) (RolePage, error) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []Role
	for roleName, members := range s.roles {
		if role != "" {
			if strings.EqualFold(search, "blur") {
				if !strings.Contains(strings.ToLower(roleName), strings.ToLower(role)) {
					continue
				}
			} else if roleName != role {
				continue
			}
		}
		for _, name := range members {
			if username != "" && name != username {
				continue
			}
			matches = append(matches, Role{Role: roleName, Username: name})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Role != matches[j].Role {
			return matches[i].Role < matches[j].Role
		}
		return matches[i].Username < matches[j].Username
	})

	total := len(matches)
	pages := (total + pageSize - 1) / pageSize
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return RolePage{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      matches[start:end],
	}, nil
}

// SearchRoles returns role names matching the substring.
func (s *Service) SearchRoles(prefix string) ([]string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for role := range s.roles {
		if prefix == "" || strings.Contains(strings.ToLower(role), prefix) {
			out = append(out, role)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CreatePermission binds a permission to a role.
func (s *Service) CreatePermission(role, resource, action string) error {
	if err := validatePermission(role, resource, action); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.roles[role]; !ok {
		return ErrRoleNotFound
	}
	for _, p := range s.permissions[role] {
		if p.Resource == resource && p.Action == action {
			return ErrPermissionExists
		}
	}
	s.permissions[role] = append(s.permissions[role], Permission{Role: role, Resource: resource, Action: action})
	return nil
}

// DeletePermission removes a permission from a role.
func (s *Service) DeletePermission(role, resource, action string) error {
	if err := validatePermission(role, resource, action); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	perms := s.permissions[role]
	for i, p := range perms {
		if p.Resource == resource && p.Action == action {
			s.permissions[role] = append(perms[:i], perms[i+1:]...)
			return nil
		}
	}
	return ErrPermissionNotFound
}

// ListPermissions returns a paginated permission list filtered by role.
func (s *Service) ListPermissions(pageNo, pageSize int, role, search string) (PermissionPage, error) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matches []Permission
	for roleName, perms := range s.permissions {
		if role != "" {
			if strings.EqualFold(search, "blur") {
				if !strings.Contains(strings.ToLower(roleName), strings.ToLower(role)) {
					continue
				}
			} else if roleName != role {
				continue
			}
		}
		matches = append(matches, perms...)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Role != matches[j].Role {
			return matches[i].Role < matches[j].Role
		}
		if matches[i].Resource != matches[j].Resource {
			return matches[i].Resource < matches[j].Resource
		}
		return matches[i].Action < matches[j].Action
	})

	total := len(matches)
	pages := (total + pageSize - 1) / pageSize
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return PermissionPage{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      matches[start:end],
	}, nil
}

// HasPermission reports whether a permission is already bound to a role.
func (s *Service) HasPermission(role, resource, action string) (bool, error) {
	if err := validatePermission(role, resource, action); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.permissions[role] {
		if p.Resource == resource && p.Action == action {
			return true, nil
		}
	}
	return false, nil
}

// Login authenticates a user and returns an access token with metadata.
func (s *Service) Login(username, password string) (LoginResult, error) {
	if err := validateCredentials(username, password); err != nil {
		return LoginResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[username]
	if !ok || !user.Enabled {
		return LoginResult{}, ErrInvalidCredentials
	}
	if !verifyPassword(password, user.Salt, user.Password) {
		return LoginResult{}, ErrInvalidCredentials
	}
	// Transparently migrate legacy SHA-256 hashes to bcrypt on successful
	// login. This upgrades security without requiring a password reset:
	// the user types their password, we verify against the legacy hash,
	// and if it matches we re-hash with bcrypt and persist the upgrade.
	// A failed migration (rare — bcrypt only fails on empty/overlong
	// input) does not block the login; the next login retries.
	if isLegacyHash(user.Password) {
		if newSalt, newHash, err := hashPassword(password); err == nil {
			user.Salt = newSalt
			user.Password = newHash
			s.users[username] = user
		}
	}
	roles := s.userRoles[username]
	token, err := s.tokens.issue(username, user.GlobalAdmin, roles, DefaultTokenTTL)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		AccessToken: token,
		TokenTTL:    DefaultTokenTTL,
		GlobalAdmin: user.GlobalAdmin,
		Username:    username,
	}, nil
}

// LoginResult is the Nacos-compatible login response.
type LoginResult struct {
	AccessToken string `json:"accessToken"`
	TokenTTL    int    `json:"tokenTtl"`
	GlobalAdmin bool   `json:"globalAdmin"`
	Username    string `json:"username"`
}

// VerifyToken validates a token string and returns the claims.
func (s *Service) VerifyToken(token string) (Claims, error) {
	return s.tokens.verify(token)
}

// Authorize checks whether the given claims may perform action on resource.
// Admin users bypass the check. Resource format:
//   - "{namespaceId}:{group}:{signType}/{resourceName}" for config/naming
//   - "console/users", "console/roles", "console/permissions" for console auth
func (s *Service) Authorize(claims Claims, resource, action string) error {
	if claims.GlobalAdmin {
		return nil
	}
	if resource == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for role := range claims.Roles {
		for _, p := range s.permissions[role] {
			if p.Resource != resource {
				continue
			}
			if p.Action == action || p.Action == "rw" || p.Action == "" {
				return nil
			}
		}
	}
	return ErrAccessDenied
}

// HasGlobalAdmin returns whether the user holds the ROLE_ADMIN binding.
func (s *Service) HasGlobalAdmin(username string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roles := s.userRoles[username]
	return roles[AdminRole]
}

// GetRoles returns all roles bound to a user.
func (s *Service) GetRoles(username string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roles := s.userRoles[username]
	out := make([]string, 0, len(roles))
	for role := range roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func validateCredentials(username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return ErrMissingUsername
	}
	if len(username) > 64 {
		return ErrInvalidUsernameFormat
	}
	if password == "" {
		return ErrMissingPassword
	}
	if len(password) < 4 || len(password) > 128 {
		return ErrInvalidPasswordFormat
	}
	return nil
}

func validatePermission(role, resource, action string) error {
	if role = strings.TrimSpace(role); role == "" {
		return ErrMissingRole
	}
	if resource = strings.TrimSpace(resource); resource == "" {
		return ErrMissingResource
	}
	if action = strings.TrimSpace(action); action == "" {
		return ErrMissingAction
	}
	switch action {
	case "r", "w", "rw", "read", "write":
	default:
		return fmt.Errorf("invalid action %q", action)
	}
	return nil
}

func createUserLocked(username, password string, globalAdmin bool) (*User, error) {
	salt, hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	return &User{
		Username:    username,
		Password:    hash,
		Salt:        salt,
		Enabled:     true,
		GlobalAdmin: globalAdmin,
	}, nil
}

// Password hash format:
//   - New passwords: "bcrypt$<bcrypt-hash>" — bcrypt includes its own salt
//     and a per-hash cost factor, so the Salt field on User is unused for
//     bcrypt hashes (kept for snapshot backward compatibility).
//   - Legacy snapshots: 64-char hex SHA-256 hash with a separate 32-char
//     hex salt. Single-iteration SHA-256 is fast enough that a leaked
//     snapshot is brute-forceable on commodity GPUs; this format is kept
//     only so existing snapshots verify, and is migrated to bcrypt on
//     the next successful login (see [Service.Login]).
const (
	// hashPrefixBcrypt identifies bcrypt-hashed passwords. Legacy SHA-256
	// hashes have no prefix.
	hashPrefixBcrypt = "bcrypt$"
)

// bcryptCost is the work factor for new bcrypt hashes. The default (12) is
// lazily overridable via the GONACOS_BCRYPT_COST env var so tests can
// lower it to bcrypt.MinCost (4) for fast iteration without changing
// production security. 12 strikes a balance: ~250ms verify on modern
// hardware, slow enough to make offline brute-force painful, fast enough
// that interactive login does not feel laggy.
var (
	bcryptCostOnce sync.Once
	bcryptCostVal  int
)

func bcryptCost() int {
	bcryptCostOnce.Do(func() {
		bcryptCostVal = 12
		if v := os.Getenv("GONACOS_BCRYPT_COST"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= bcrypt.MinCost && n <= bcrypt.MaxCost {
				bcryptCostVal = n
			}
		}
	})
	return bcryptCostVal
}

// hashPassword generates a bcrypt hash of the password. Returns salt=""
// (bcrypt includes its own salt) and hash="bcrypt$<bcrypt-hash>".
func hashPassword(password string) (string, string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost())
	if err != nil {
		return "", "", err
	}
	return "", hashPrefixBcrypt + string(h), nil
}

// verifyPassword returns true if password matches the stored hash. Dispatches
// on the hash format: bcrypt$-prefixed uses bcrypt; plain hex uses the
// legacy SHA-256 scheme for backward compatibility with existing snapshots.
// Both paths use constant-time comparison.
func verifyPassword(password, salt, hash string) bool {
	if strings.HasPrefix(hash, hashPrefixBcrypt) {
		bhash := strings.TrimPrefix(hash, hashPrefixBcrypt)
		return bcrypt.CompareHashAndPassword([]byte(bhash), []byte(password)) == nil
	}
	candidate := sha256Hex(salt + password)
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(hash)) == 1
}

// isLegacyHash returns true if the stored hash is in the pre-bcrypt SHA-256
// format (no algorithm prefix). Login uses this to decide whether to
// transparently re-hash the password with bcrypt after a successful verify.
func isLegacyHash(hash string) bool {
	return !strings.HasPrefix(hash, hashPrefixBcrypt)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// tokenManager issues and verifies HMAC-SHA256 JWT-like tokens.
type tokenManager struct {
	mu     sync.Mutex
	secret []byte
	// revokedByUser tracks per-user revocation generations. A token issued
	// with a Gen strictly less than the recorded generation is rejected on
	// verify. The generation is a monotonically increasing counter to avoid
	// sub-second timestamp collisions.
	revokedByUser map[string]int64
	// nextGen is the next revocation generation to hand out.
	nextGen int64
}

func newTokenManager(secret string) *tokenManager {
	return &tokenManager{secret: []byte(secret), revokedByUser: map[string]int64{}}
}

// Claims is the verified token payload.
type Claims struct {
	Username    string          `json:"username"`
	GlobalAdmin bool            `json:"admin"`
	Roles       map[string]bool `json:"roles"`
	IssuedAt    int64           `json:"iat"`
	ExpiresAt   int64           `json:"exp"`
	Gen         int64           `json:"gen,omitempty"`
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

func (t *tokenManager) issue(username string, globalAdmin bool, roles map[string]bool, ttlSeconds int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	header := tokenHeader{Alg: "HS256", Typ: "JWT"}
	headerJSON, _ := json.Marshal(header)
	tokenRoles := make(map[string]bool, len(roles))
	for k, v := range roles {
		tokenRoles[k] = v
	}
	if globalAdmin {
		tokenRoles[AdminRole] = true
	}
	claims := Claims{
		Username:    username,
		GlobalAdmin: globalAdmin,
		Roles:       tokenRoles,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(time.Duration(ttlSeconds) * time.Second).Unix(),
		Gen:         t.nextGen,
	}
	claimsJSON, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	sig := t.sign(encoded)
	return encoded + "." + sig, nil
}

func (t *tokenManager) verify(token string) (Claims, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	encoded := parts[0] + "." + parts[1]
	expectedSig := t.sign(encoded)
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expectedSig)) != 1 {
		return Claims{}, ErrInvalidToken
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return Claims{}, ErrExpiredToken
	}
	t.mu.Lock()
	gen := t.revokedByUser[claims.Username]
	t.mu.Unlock()
	if claims.Gen < gen {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func (t *tokenManager) revokeUser(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextGen++
	t.revokedByUser[username] = t.nextGen
}

func (t *tokenManager) sign(encoded string) string {
	mac := hmac.New(sha256.New, t.secret)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ParseAuthorization extracts the bearer token from an Authorization header.
func ParseAuthorization(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if !strings.HasPrefix(header, TokenPrefix) {
		return header
	}
	return strings.TrimSpace(strings.TrimPrefix(header, TokenPrefix))
}
