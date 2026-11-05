// Package projectedauth authenticates possession of this Pod's projected token.
// The deployment and kubelet projection are the trust root, not JWT signatures.
package projectedauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

const (
	// DefaultTokenFile is the separate local-auth projection, not the IDaaS bootstrap token.
	DefaultTokenFile = "/run/secrets/idaas-akless/credential-auth/token"
	// Audience must match only the local credential-auth projection.
	Audience = "idaas.akless.sidecar"
	// MaxTokenBytes bounds both the reference file and request header token.
	MaxTokenBytes = 8192
	previousGrace = 60 * time.Second
	// Cap the age of both current and previous tokens, independently of JWT exp.
	// Keep the local authentication projection expirationSeconds within 600–3600.
	maxLifetime = time.Hour
	clockSkew   = 30 * time.Second
)

var (
	ErrInvalidRequest = errors.New("invalid credential authentication header")
	ErrUnauthorized   = errors.New("credential authentication required")
	ErrUnavailable    = errors.New("credential authentication unavailable")
)

// PodContext must come from the trusted Pod declaration (Downward API).
type PodContext struct {
	UID, Namespace, ServiceAccount string
}

type referenceClaims struct {
	jwt.RegisteredClaims
	Kubernetes struct {
		Namespace string `json:"namespace"`
		Pod       struct {
			UID string `json:"uid"`
		} `json:"pod"`
		ServiceAccount struct {
			Name string `json:"name"`
		} `json:"serviceaccount"`
	} `json:"kubernetes.io"`
}

type record struct {
	digest   [sha256.Size]byte
	until    time.Time
	issuedAt time.Time
}

// Authenticator retains only two digests. It never writes tokens or calls an API.
type Authenticator struct {
	mu                sync.Mutex
	root              *os.Root
	requireRootOwner  bool
	name              string
	pod               PodContext
	now               func() time.Time
	current, previous record
	lastFailureReason referenceFailure
	lastFailureLogAt  time.Time
}

// New opens a caller-trusted directory and requires a valid initial reference.
// It checks token shape, permissions, Pod binding and time claims, but does not
// enforce a read-only mount or root ownership. Container startup uses NewProjected.
func New(path string, pod PodContext) (*Authenticator, error) {
	if !filepath.IsAbs(path) || pod.UID == "" || pod.Namespace == "" || pod.ServiceAccount == "" {
		return nil, reasonInvalidConfiguration
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, referenceIOFailure(err)
	}
	a := &Authenticator{root: root, name: filepath.Base(path), pod: pod, now: time.Now}
	if err := a.reload(a.now()); err != nil {
		_ = root.Close()
		return nil, err
	}
	return a, nil
}

// Close releases the pinned directory; callers must first drain HTTP requests.
func (a *Authenticator) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.root.Close()
}

// Authenticate returns a final check to run immediately before releasing STS.
func (a *Authenticator) Authenticate(r *http.Request) (func() error, error) {
	var values []string
	for key, value := range r.Header {
		if strings.EqualFold(key, "Authorization") {
			values = append(values, value...)
		}
	}
	if len(values) == 0 {
		return nil, ErrUnauthorized
	}
	if len(values) != 1 || !canonicalJWT(values[0]) {
		return nil, ErrInvalidRequest
	}
	digest := sha256.Sum256([]byte(values[0]))
	if err := a.check(digest); err != nil {
		return nil, err
	}
	return func() error { return a.check(digest) }, nil
}

func (a *Authenticator) check(digest [sha256.Size]byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if err := a.reload(now); err != nil {
		return err
	}
	current := subtle.ConstantTimeCompare(digest[:], a.current.digest[:])
	previous := subtle.ConstantTimeCompare(digest[:], a.previous.digest[:])
	if current == 1 && now.Before(a.current.until) || previous == 1 && now.Before(a.previous.until) {
		return nil
	}
	return ErrUnauthorized
}

// reload and state commit run under mu (or during construction before publication).
func (a *Authenticator) reload(now time.Time) error {
	next, err := a.read(now)
	a.logReferenceStatus(now, err)
	if err != nil {
		a.current, a.previous = record{}, record{}
		return err
	}
	if a.current.digest != next.digest {
		a.previous = a.current
		for _, end := range []time.Time{now.Add(previousGrace), next.issuedAt.Add(previousGrace)} {
			if a.previous.until.After(end) {
				a.previous.until = end
			}
		}
		a.current = next
	}
	return nil
}

func (a *Authenticator) read(now time.Time) (record, error) {
	// O_NONBLOCK prevents a substituted FIFO from blocking before f.Stat.
	// Root.OpenFile follows kubelet links without escaping the trusted directory.
	f, err := a.root.OpenFile(a.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return record{}, referenceIOFailure(err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return record{}, referenceIOFailure(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || info.Size() > MaxTokenBytes {
		return record{}, reasonInvalidFile
	}
	if !safeTokenPermissions(info.Mode(), stat.Uid, os.Geteuid()) || a.requireRootOwner && stat.Uid != 0 {
		return record{}, reasonUnsafePermissions
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxTokenBytes+1))
	if err != nil {
		return record{}, referenceIOFailure(err)
	}
	if !canonicalJWT(string(data)) {
		return record{}, reasonInvalidToken
	}
	digest := sha256.Sum256(data)
	if digest == a.current.digest && now.Before(a.current.until) {
		return a.current, nil
	}
	var claims referenceClaims
	// Parse only the trusted file. This deliberately does NOT verify a signature.
	if _, _, err := new(jwt.Parser).ParseUnverified(string(data), &claims); err != nil {
		return record{}, reasonInvalidToken
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.NotBefore == nil {
		return record{}, reasonInvalidValidity
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != Audience ||
		claims.Subject != "system:serviceaccount:"+a.pod.Namespace+":"+a.pod.ServiceAccount ||
		claims.Kubernetes.Namespace != a.pod.Namespace || claims.Kubernetes.Pod.UID != a.pod.UID ||
		claims.Kubernetes.ServiceAccount.Name != a.pod.ServiceAccount {
		return record{}, reasonInvalidBinding
	}
	until := claims.ExpiresAt.Time
	if cap := claims.IssuedAt.Add(maxLifetime); until.After(cap) {
		until = cap
	}
	if claims.IssuedAt.After(now.Add(clockSkew)) || claims.NotBefore.After(now.Add(clockSkew)) ||
		!until.After(claims.NotBefore.Time) {
		return record{}, reasonInvalidValidity
	}
	if !now.Before(claims.ExpiresAt.Time) {
		return record{}, reasonExpired
	}
	if !now.Before(until) {
		return record{}, reasonMaximumAgeExceeded
	}
	return record{digest: digest, until: until, issuedAt: claims.IssuedAt.Time}, nil
}

// Kubelet may create root-owned 0640 tokens when fsGroup is configured, even
// with defaultMode 0440. Only root may retain owner-write; neither the service
// user nor any group/other user may have write permission. Mounts stay read-only.
func safeTokenPermissions(mode os.FileMode, owner uint32, euid int) bool {
	writes := mode.Perm() & 0222
	return writes == 0 || writes == 0200 && owner == 0 && euid != 0
}

func canonicalJWT(token string) bool {
	if len(token) == 0 || len(token) > MaxTokenBytes {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}
