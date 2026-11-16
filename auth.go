package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const sessionName = "fm_session"
const sessionLifetime = 12 * time.Hour

type credentials struct {
	Salt []byte `json:"salt"`
	Hash []byte `json:"hash"`
}
type authStore struct {
	creds       credentials
	secure      bool
	mu          sync.Mutex
	sessions    map[[32]byte]time.Time
	attempts    chan struct{}
	lastCleanup time.Time
	failures    int
	retryAfter  time.Time
}

func newAuth(state string, secure, reset bool) (*authStore, error) {
	name := filepath.Join(state, "credentials.json")
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	var creds credentials
	if err == nil {
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
			return nil, errors.New("credentials.json must be a private regular file")
		}
		if !reset {
			if err := json.NewDecoder(f).Decode(&creds); err != nil || len(creds.Salt) != 32 || len(creds.Hash) != 32 {
				return nil, errors.New("invalid credentials.json")
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	if len(creds.Hash) == 0 {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return nil, errors.New(cli("initialization requires an interactive terminal to set the admin password", "首次设置管理员密码需要交互式终端"))
		}
		first, err := readPassword(cli("Set password for admin (at least 12 characters): ", "设置 admin 密码（至少 12 个字符）："))
		if err != nil {
			return nil, err
		}
		if len([]rune(string(first))) < 12 || len(first) > 1024 {
			return nil, errors.New(cli("password must contain at least 12 characters and at most 1024 bytes", "密码至少需要 12 个字符，最多 1024 字节"))
		}
		second, err := readPassword(cli("Repeat password: ", "再次输入密码："))
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(first, second) != 1 {
			return nil, errors.New(cli("passwords do not match", "两次输入的密码不一致"))
		}
		creds.Salt = make([]byte, 32)
		rand.Read(creds.Salt)
		creds.Hash = derive(string(first), creds.Salt)
		f, err := os.CreateTemp(state, ".credential-")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		err = json.NewEncoder(f).Encode(creds)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if reset {
			err = os.Rename(f.Name(), name)
		} else {
			err = os.Link(f.Name(), name)
		}
		if err != nil {
			return nil, fmt.Errorf("publish credentials: %w", err)
		}
	}
	return &authStore{creds: creds, secure: secure, sessions: make(map[[32]byte]time.Time), attempts: make(chan struct{}, 2)}, nil
}

func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	p, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return p, err
}

func derive(password string, salt []byte) []byte {
	hash, _ := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	return hash
}

func (a *authStore) hasSession(r *http.Request) bool {
	c, err := r.Cookie(sessionName)
	if err != nil || len(c.Value) != 64 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.sessions[sha256.Sum256([]byte(c.Value))])
}

func (a *authStore) cookie(w http.ResponseWriter, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: sessionName, Value: value, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: age})
}

func (a *authStore) login(w http.ResponseWriter, r *http.Request) error {
	var input struct{ Username, Password string }
	if err := parseJSON(w, r, &input); err != nil {
		return err
	}
	select {
	case a.attempts <- struct{}{}:
		defer func() { <-a.attempts }()
	default:
		return failure(429, "rate")
	}
	a.mu.Lock()
	blocked := time.Now().Before(a.retryAfter)
	a.mu.Unlock()
	if blocked {
		return failure(429, "rate")
	}
	if len(input.Password) > 1024 || subtle.ConstantTimeCompare(derive(input.Password, a.creds.Salt), a.creds.Hash) != 1 || input.Username != "admin" {
		a.mu.Lock()
		a.failures++
		if a.failures >= 5 {
			a.retryAfter = time.Now().Add(15 * time.Second)
			a.failures = 0
		}
		a.mu.Unlock()
		return failure(401, "credentials")
	}
	var token [32]byte
	rand.Read(token[:])
	value := hex.EncodeToString(token[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.lastCleanup) > time.Minute {
		for id, expiry := range a.sessions {
			if !now.Before(expiry) {
				delete(a.sessions, id)
			}
		}
		a.lastCleanup = now
	}
	if c, err := r.Cookie(sessionName); err == nil {
		delete(a.sessions, sha256.Sum256([]byte(c.Value)))
	}
	a.failures = 0
	a.sessions[sha256.Sum256([]byte(value))] = now.Add(sessionLifetime)
	a.cookie(w, value, int(sessionLifetime.Seconds()))
	return respond(w, map[string]string{"user": "admin"})
}

func (a *authStore) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionName); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(c.Value)))
		a.mu.Unlock()
	}
	a.cookie(w, "", -1)
	return respond(w, map[string]bool{"ok": true})
}
