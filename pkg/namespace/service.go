package namespace

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	PublicID   = "public"
	PublicName = "public"
)

var (
	ErrMissingNamespaceID   = errors.New("namespaceId is required")
	ErrMissingNamespaceName = errors.New("namespaceName is required")
	ErrInvalidNamespaceID   = errors.New("too long namespaceId, length should not exceed 128")
	ErrInvalidNamespaceName = errors.New("invalid namespaceName")
	ErrNamespaceExists      = errors.New("namespace already exists")
	ErrNamespaceNotFound    = errors.New("namespace not found")
	ErrDeletePublic         = errors.New("public namespace cannot be deleted")

	namespaceNameRe = regexp.MustCompile(`^[\p{L}\p{N}_\-. ]+$`)
)

type Namespace struct {
	Namespace         string `json:"namespace"`
	NamespaceShowName string `json:"namespaceShowName"`
	NamespaceDesc     string `json:"namespaceDesc"`
	Quota             int    `json:"quota"`
	ConfigCount       int    `json:"configCount"`
	Type              int    `json:"type"`
}

type Service struct {
	mu         sync.RWMutex
	namespaces map[string]Namespace
}

func NewService() *Service {
	s := &Service{
		namespaces: map[string]Namespace{},
	}
	s.namespaces[PublicID] = Namespace{
		Namespace:         PublicID,
		NamespaceShowName: PublicName,
		NamespaceDesc:     "Default Namespace",
		Quota:             200,
		ConfigCount:       0,
		Type:              0,
	}
	return s
}

func (s *Service) Create(namespaceID, name, desc string) error {
	namespaceID, name = strings.TrimSpace(namespaceID), strings.TrimSpace(name)
	if err := validateIdentity(namespaceID, name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.namespaces[namespaceID]; ok {
		return ErrNamespaceExists
	}
	s.namespaces[namespaceID] = Namespace{
		Namespace:         namespaceID,
		NamespaceShowName: name,
		NamespaceDesc:     desc,
		Quota:             200,
		ConfigCount:       0,
		Type:              2,
	}
	return nil
}

func (s *Service) Update(namespaceID, name, desc string) error {
	namespaceID, name = strings.TrimSpace(namespaceID), strings.TrimSpace(name)
	if err := validateIdentity(namespaceID, name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ns, ok := s.namespaces[namespaceID]
	if !ok {
		return ErrNamespaceNotFound
	}
	ns.NamespaceShowName = name
	ns.NamespaceDesc = desc
	s.namespaces[namespaceID] = ns
	return nil
}

func (s *Service) Delete(namespaceID string) error {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID == "" {
		return ErrMissingNamespaceID
	}
	if namespaceID == PublicID {
		return ErrDeletePublic
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.namespaces[namespaceID]; !ok {
		return ErrNamespaceNotFound
	}
	delete(s.namespaces, namespaceID)
	return nil
}

func (s *Service) Get(namespaceID string) (Namespace, error) {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID == "" {
		return Namespace{}, ErrMissingNamespaceID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ns, ok := s.namespaces[namespaceID]
	if !ok {
		return Namespace{}, ErrNamespaceNotFound
	}
	return ns, nil
}

func (s *Service) Exists(namespaceID string) bool {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.namespaces[namespaceID]
	return ok
}

func (s *Service) List() []Namespace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Namespace, 0, len(s.namespaces))
	for _, ns := range s.namespaces {
		items = append(items, ns)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Namespace < items[j].Namespace
	})
	return items
}

func validateIdentity(namespaceID, name string) error {
	if namespaceID == "" {
		return ErrMissingNamespaceID
	}
	if name == "" {
		return ErrMissingNamespaceName
	}
	if len(namespaceID) > 128 {
		return ErrInvalidNamespaceID
	}
	if len(name) > 64 || !namespaceNameRe.MatchString(name) {
		return ErrInvalidNamespaceName
	}
	return nil
}
