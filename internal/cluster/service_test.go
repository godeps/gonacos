package cluster

import (
	"errors"
	"testing"
)

func TestStandaloneModeSeedsSelfMember(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "127.0.0.1", 8848, 9848, 9849)

	if s.Mode() != ModeStandalone {
		t.Fatalf("mode = %v, want standalone", s.Mode())
	}
	self := s.Self()
	if self == nil || !self.IsSelf || self.State != "UP" {
		t.Fatalf("self = %+v", self)
	}
	if len(self.Abilities) == 0 {
		t.Fatalf("no abilities")
	}

	members := s.ListMembers()
	if len(members) != 1 || members[0].ID != self.ID {
		t.Fatalf("members = %+v", members)
	}
}

func TestStandaloneRejectsAddMember(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	_, err := s.AddMember(Member{ID: "node-2", IP: "10.0.0.2", Port: 8848})
	if !errors.Is(err, ErrNotClusterMode) {
		t.Fatalf("err = %v, want ErrNotClusterMode", err)
	}
}

func TestStandaloneCannotRemoveSelf(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)
	self := s.Self()

	if err := s.RemoveMember(self.ID); err == nil {
		t.Fatalf("removed self without error")
	}
}

func TestStandaloneRemoveMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	if err := s.RemoveMember("missing"); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("err = %v, want ErrMemberNotFound", err)
	}
}

func TestStandaloneGetMember(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)
	self := s.Self()

	got, err := s.GetMember(self.ID)
	if err != nil || got.ID != self.ID {
		t.Fatalf("get = %v %v", got, err)
	}
	if _, err := s.GetMember(""); !errors.Is(err, ErrMissingMemberID) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := s.GetMember("missing"); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestPluginCRUD(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	plugins := s.ListPlugins()
	if len(plugins) == 0 {
		t.Fatalf("no plugins")
	}

	p, err := s.GetPlugin("nacos-default")
	if err != nil || !p.Enabled {
		t.Fatalf("get default: %v %+v", err, p)
	}

	p, err = s.UpdatePluginStatus("nacos-default", false)
	if err != nil || p.Enabled {
		t.Fatalf("disable: %v %+v", err, p)
	}

	p, err = s.UpdatePluginConfig("nacos-default", map[string]string{"k": "v"})
	if err != nil || p.Config["k"] != "v" {
		t.Fatalf("config: %v %+v", err, p)
	}

	if _, err := s.UpdatePluginStatus("missing", true); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("missing plugin: %v", err)
	}
	if _, err := s.UpdatePluginConfig("", nil); !errors.Is(err, ErrMissingPluginID) {
		t.Fatalf("empty id: %v", err)
	}
}

func TestLogLevelDefaultsAndUpdates(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	if s.LogLevel() != "INFO" {
		t.Fatalf("default = %v, want INFO", s.LogLevel())
	}
	s.SetLogLevel("debug")
	if s.LogLevel() != "DEBUG" {
		t.Fatalf("after set = %v, want DEBUG", s.LogLevel())
	}
	s.SetLogLevel("")
	if s.LogLevel() != "DEBUG" {
		t.Fatalf("empty set should not change: %v", s.LogLevel())
	}
}

func TestRaftOpsStandaloneReturnsUnavailable(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	result, err := s.RaftOps("snapshot", "group-1")
	if err != nil {
		t.Fatalf("raft ops: %v", err)
	}
	if result["available"] != false {
		t.Fatalf("raft available in standalone: %+v", result)
	}
}

func TestLoaderStubsReturnCounts(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	metrics := s.LoaderMetrics()
	if metrics["mode"] != "standalone" {
		t.Fatalf("metrics = %+v", metrics)
	}
	if metrics["reloadCount"] != 0 {
		t.Fatalf("reloadCount = %v", metrics["reloadCount"])
	}

	clients := s.CurrentClients()
	if clients["count"] != 0 {
		t.Fatalf("clients = %+v", clients)
	}

	reload := s.SmartReload()
	if reload["reloaded"] != 0 {
		t.Fatalf("smart reload = %+v", reload)
	}

	single := s.ReloadSingle("client-1")
	if single["reloaded"] != false {
		t.Fatalf("reload single = %+v", single)
	}

	count := s.ReloadCount()
	if count["count"] != 0 {
		t.Fatalf("reload count = %+v", count)
	}
}

func TestIDsReturnsStandaloneState(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	ids := s.IDs()
	if ids["mode"] != "standalone" {
		t.Fatalf("ids = %+v", ids)
	}
	if ids["serverId"] == "" {
		t.Fatalf("serverId empty")
	}
}

func TestUpdateLookupReturnsMembers(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	members, err := s.UpdateLookup("health")
	if err != nil {
		t.Fatalf("update lookup: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}
}

func TestUpdateNodesStandaloneIgnoresInput(t *testing.T) {
	t.Parallel()
	s := NewService(ModeStandalone, "", 0, 0, 0)

	members, err := s.UpdateNodes([]Member{{ID: "node-2", IP: "10.0.0.2", Port: 8848}})
	if err != nil {
		t.Fatalf("update nodes: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("standalone should ignore extra nodes, got %d", len(members))
	}
}
