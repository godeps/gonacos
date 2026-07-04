package auth

import "sort"

// authSnapshotEntry mirrors a user row including salted hash so backups can
// restore credentials without knowing plaintext passwords.
type authSnapshotEntry struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Salt        string `json:"salt"`
	Enabled     bool   `json:"enabled"`
	GlobalAdmin bool   `json:"globalAdmin"`
	Roles       []string `json:"roles"`
}

type authPermissionEntry struct {
	Role     string `json:"role"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

type authSnapshot struct {
	Users       []authSnapshotEntry    `json:"users"`
	Permissions []authPermissionEntry  `json:"permissions"`
}

// SnapshotKey identifies the auth service in backup envelopes.
func (s *Service) SnapshotKey() string { return "auth" }

// Snapshot returns users (with salted hashes), role bindings, and
// permissions. Tokens are intentionally excluded: they are ephemeral and
// tied to the signing secret of the current process.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]authSnapshotEntry, 0, len(s.users))
	for name, u := range s.users {
		roles := make([]string, 0, len(s.userRoles[name]))
		for r := range s.userRoles[name] {
			roles = append(roles, r)
		}
		sort.Strings(roles)
		users = append(users, authSnapshotEntry{
			Username:    u.Username,
			Password:    u.Password,
			Salt:        u.Salt,
			Enabled:     u.Enabled,
			GlobalAdmin: u.GlobalAdmin,
			Roles:       roles,
		})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	perms := make([]authPermissionEntry, 0)
	for _, list := range s.permissions {
		for _, p := range list {
			perms = append(perms, authPermissionEntry(p))
		}
	}
	sort.Slice(perms, func(i, j int) bool {
		if perms[i].Role != perms[j].Role {
			return perms[i].Role < perms[j].Role
		}
		if perms[i].Resource != perms[j].Resource {
			return perms[i].Resource < perms[j].Resource
		}
		return perms[i].Action < perms[j].Action
	})
	return authSnapshot{Users: users, Permissions: perms}, nil
}

// Restore replaces all user, role, and permission state. Existing tokens are
// left intact: the signing secret is process-local, so tokens issued in this
// process remain valid for restored users with matching password hashes. A
// cross-process restore invalidates tokens via signature mismatch, not
// revocation.
func (s *Service) Restore(data any) error {
	snap, ok := data.(map[string]any)
	if !ok {
		return errAuthSnapshotShape
	}
	users, err := decodeAuthUsers(snap["users"])
	if err != nil {
		return err
	}
	perms, err := decodeAuthPermissions(snap["permissions"])
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = map[string]*User{}
	s.roles = map[string][]string{}
	s.userRoles = map[string]map[string]bool{}
	s.permissions = map[string][]Permission{}
	for _, e := range users {
		u := &User{
			Username:    e.Username,
			Password:    e.Password,
			Salt:        e.Salt,
			Enabled:     e.Enabled,
			GlobalAdmin: e.GlobalAdmin,
		}
		s.users[e.Username] = u
		roleSet := map[string]bool{}
		for _, r := range e.Roles {
			if r == "" {
				continue
			}
			roleSet[r] = true
			s.roles[r] = append(s.roles[r], e.Username)
		}
		s.userRoles[e.Username] = roleSet
	}
	for _, p := range perms {
		s.permissions[p.Role] = append(s.permissions[p.Role], Permission(p))
	}
	return nil
}

func decodeAuthUsers(raw any) ([]authSnapshotEntry, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAuthSnapshotShape
	}
	out := make([]authSnapshotEntry, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAuthSnapshotShape
		}
		e := authSnapshotEntry{}
		if v, ok := m["username"].(string); ok {
			e.Username = v
		}
		if v, ok := m["password"].(string); ok {
			e.Password = v
		}
		if v, ok := m["salt"].(string); ok {
			e.Salt = v
		}
		if v, ok := m["enabled"].(bool); ok {
			e.Enabled = v
		}
		if v, ok := m["globalAdmin"].(bool); ok {
			e.GlobalAdmin = v
		}
		if raw, ok := m["roles"].([]any); ok {
			e.Roles = make([]string, 0, len(raw))
			for _, r := range raw {
				if s, ok := r.(string); ok {
					e.Roles = append(e.Roles, s)
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func decodeAuthPermissions(raw any) ([]authPermissionEntry, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errAuthSnapshotShape
	}
	out := make([]authPermissionEntry, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errAuthSnapshotShape
		}
		p := authPermissionEntry{}
		if v, ok := m["role"].(string); ok {
			p.Role = v
		}
		if v, ok := m["resource"].(string); ok {
			p.Resource = v
		}
		if v, ok := m["action"].(string); ok {
			p.Action = v
		}
		out = append(out, p)
	}
	return out, nil
}

var errAuthSnapshotShape = snapshotShapeError("auth snapshot shape mismatch")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
