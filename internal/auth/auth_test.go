package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHashLookup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "keys.yaml")
	plain := "sk-live-3f1c0e7a9b2d4c88"
	if err := os.WriteFile(p, []byte("keys:\n  - name: a\n    key: "+plain+"\n    models: [\"*\"]\n    rpm: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	k, ok := s.Lookup(plain)
	if !ok || k.Name != "a" {
		t.Fatalf("%v %v", ok, k)
	}
	if _, ok := s.Lookup("sk-wrong"); ok {
		t.Fatal("wrong key accepted")
	}
}

func TestRPMTumbling(t *testing.T) {
	s, _ := Load("", []string{"sk-testkey0000000000000000"}, false)
	k, _ := s.Lookup("sk-testkey0000000000000000")
	for i := 0; i < 3; i++ {
		_, ok := s.AllowRPM(k.Hash, 3)
		if !ok {
			t.Fatal(i)
		}
	}
	if _, ok := s.AllowRPM(k.Hash, 3); ok {
		t.Fatal("expected deny")
	}
	s.mu.Lock()
	s.rpm[k.Hash].window = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()
	if _, ok := s.AllowRPM(k.Hash, 3); !ok {
		t.Fatal("window should reset")
	}
}

func TestRPMUnlimited(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "keys.yaml")
	plain := "sk-live-unlimited000000000000"
	if err := os.WriteFile(p, []byte("keys:\n  - name: a\n    key: "+plain+"\n    rpm: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	k, ok := s.Lookup(plain)
	if !ok || k.RPM != 0 {
		t.Fatalf("rpm %d ok %v", k.RPM, ok)
	}
	for i := 0; i < 100; i++ {
		if _, ok := s.AllowRPM(k.Hash, k.RPM); !ok {
			t.Fatal(i)
		}
	}
}

func TestBearer(t *testing.T) {
	if BearerToken("Bearer sk-abc", "") != "sk-abc" {
		t.Fatal(BearerToken("Bearer sk-abc", ""))
	}
	if BearerToken("", "sk-xyz") != "sk-xyz" {
		t.Fatal(BearerToken("", "sk-xyz"))
	}
}
