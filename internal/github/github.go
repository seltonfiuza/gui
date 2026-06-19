// Package github wraps the gh CLI and a PAT/keychain fallback for auth and PR
// flows. See docs/specs/02-github.md. No UI dependencies.
package github

import (
	"errors"

	"github.com/selton/gui/internal/git"
)

// Host is a GitHub hostname (github.com or an enterprise host).
type Host struct {
	Hostname string
}

// DefaultHost returns the github.com host.
func DefaultHost() Host { return Host{Hostname: "github.com"} }

// HostForRemote returns the host for a remote, defaulting to github.com.
func HostForRemote(r *git.Remote) Host {
	if r != nil && r.Host != "" {
		return Host{Hostname: r.Host}
	}
	return DefaultHost()
}

// AuthState describes auth for a host.
type AuthState struct {
	Hostname      string
	Authenticated bool
	Login         string
	Source        string // "gh" | "keychain" | ""
}

// PR is a pull request summary/detail.
type PR struct {
	Number        int
	Title         string
	State         string
	Author        string
	HeadRef       string
	BaseRef       string
	URL           string
	Body          string
	Draft         bool
	ChecksSummary string
}

// CreatePROpts are inputs to CreatePR.
type CreatePROpts struct {
	Title string
	Body  string
	Head  string
	Base  string
	Draft bool
}

// ErrNoToken indicates no token is available for the host.
var ErrNoToken = errors.New("no github token for host")

// Service performs GitHub operations for a host.
type Service struct {
	host Host
}

// New builds a Service for host.
func New(host Host) *Service { return &Service{host: host} }

func (s *Service) AuthStatus() (AuthState, error) { panic("not implemented") }
func (s *Service) Token() (string, error)         { panic("not implemented") }
func (s *Service) SetToken(pat string) error      { panic("not implemented") }
func (s *Service) ClearToken() error              { panic("not implemented") }

func (s *Service) ListPRs(repo *git.Remote) ([]PR, error)            { panic("not implemented") }
func (s *Service) ViewPR(repo *git.Remote, number int) (PR, error)   { panic("not implemented") }
func (s *Service) CreatePR(repo *git.Remote, o CreatePROpts) (PR, error) { panic("not implemented") }
