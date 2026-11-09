package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSessionLeaseAndPrivateMetadata(t *testing.T) {
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := randomID("s_")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.create(id); err != nil {
		t.Fatal(err)
	}
	lease, err := s.lock(id)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if other, err := s.lock(id); err == nil {
		other.Close()
		t.Fatal("concurrent worker acquired the same session")
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal(err)
	}
	state := Snapshot{SchemaVersion: protocolVersion, ID: id, Instance: "instance", State: "waiting_input", Pending: []RequestInfo{{ID: "q1", Method: "input", Parameters: json.RawMessage(`{"message":"private question"}`)}}}
	if err := s.save(state); err != nil {
		t.Fatal(err)
	}
	if len(state.Pending[0].Parameters) == 0 {
		t.Fatal("saving metadata mutated the live question")
	}
	dir, _ := s.dir(id)
	path := filepath.Join(dir, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private question")) || bytes.Contains(data, []byte("params")) {
		t.Fatalf("question was persisted: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("metadata permissions: %v", info.Mode())
	}
	loaded, err := s.load(id)
	if err != nil || loaded.Pending[0].ID != "q1" {
		t.Fatalf("metadata read: %+v %v", loaded, err)
	}
}
