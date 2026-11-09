package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var sessionID = regexp.MustCompile(`^s_[0-9a-f]{24}$`)

type store struct{ home string }

func newStore(home string) (store, error) {
	abs, err := filepath.Abs(home)
	return store{home: abs}, err
}

func randomID(prefix string) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("session directory must be owned by the current user with mode 0700: %s", path)
	}
	return nil
}

func (s store) dir(id string) (string, error) {
	if !sessionID.MatchString(id) {
		return "", fmt.Errorf("invalid session ID %q", id)
	}
	return filepath.Join(s.home, "sessions", id), nil
}

func (s store) create(id string) error {
	if err := privateDir(filepath.Join(s.home, "sessions")); err != nil {
		return err
	}
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	return os.Mkdir(dir, 0700)
}

func (s store) load(id string) (Snapshot, error) {
	var result Snapshot
	dir, err := s.dir(id)
	if err != nil {
		return result, err
	}
	f, err := os.Open(filepath.Join(dir, "session.json"))
	if err != nil {
		return result, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("invalid session metadata")
	}
	if result.SchemaVersion != protocolVersion || result.ID != id || result.Instance == "" {
		return result, fmt.Errorf("invalid session metadata for %s", id)
	}
	return result, nil
}

func (s store) save(state Snapshot) error {
	// 断线重连可读取问题全文；落盘仅保存待处理请求摘要，不复制用户输入或工具内容。
	state.Pending = append([]RequestInfo(nil), state.Pending...)
	for i := range state.Pending {
		state.Pending[i].Parameters = nil
	}
	dir, err := s.dir(state.ID)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(state); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "session.json"))
}

func (s store) lock(id string) (*os.File, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("session %s already has a worker: %w", id, err)
	}
	return f, nil
}

func (s store) socket(state Snapshot) (string, error) {
	// Unix socket 路径有长度限制；元数据仍保存在 HYCLI_HOME，socket 放入独占的短路径目录。
	root := filepath.Join("/tmp", fmt.Sprintf("hycli-%d", os.Getuid()))
	if err := privateDir(root); err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(s.home + "\x00" + state.ID + "\x00" + state.Instance))
	return filepath.Join(root, hex.EncodeToString(hash[:16])+".sock"), nil
}
