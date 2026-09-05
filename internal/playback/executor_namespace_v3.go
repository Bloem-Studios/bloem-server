package playback

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
)

var ErrInvalidExecutorNamespace = errors.New("invalid executor namespace")
var ErrExecutorNamespaceMismatch = errors.New("executor namespace mismatch")
var ErrExecutorReplacementRequired = errors.New("fresh executor binding required for replacement")

// ExecutorNamespaceV3 is supplied by authority. It never generates identities.
type ExecutorNamespaceV3 struct {
	Incarnation string `json:"incarnation"`
	Epoch       int64  `json:"epoch"`
	ExecutorID  string `json:"executor_id"`
}

func (n ExecutorNamespaceV3) Validate() error {
	for _, value := range []string{n.Incarnation, n.ExecutorID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return fmt.Errorf("%w: canonical nonzero UUID required", ErrInvalidExecutorNamespace)
		}
	}
	if n.Epoch <= 0 {
		return fmt.Errorf("%w: epoch must be positive", ErrInvalidExecutorNamespace)
	}
	return nil
}
func (n ExecutorNamespaceV3) OutputSubdir() (string, error) {
	if err := n.Validate(); err != nil {
		return "", err
	}
	return filepath.Join("_authority", n.Incarnation, strconv.FormatInt(n.Epoch, 10), n.ExecutorID), nil
}
func (n ExecutorNamespaceV3) OutputDir(root string) (string, error) {
	subdir, err := n.OutputSubdir()
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%w: output root required", ErrInvalidExecutorNamespace)
	}
	return filepath.Join(root, subdir), nil
}
func cloneExecutorNamespace(n *ExecutorNamespaceV3) *ExecutorNamespaceV3 {
	if n == nil {
		return nil
	}
	return new(*n)
}

// MatchExecutorNamespace rejects omission, substitution and invalid bound values.
func MatchExecutorNamespace(actual, expected *ExecutorNamespaceV3) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return ErrExecutorNamespaceMismatch
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if *actual != *expected {
		return ErrExecutorNamespaceMismatch
	}
	return nil
}

// executorOutputRoot verifies the caller already resolved the exact bound path.
func executorOutputRoot(output string, n ExecutorNamespaceV3) (string, error) {
	if err := n.Validate(); err != nil {
		return "", err
	}
	clean := filepath.Clean(output)
	root := clean
	for range 4 {
		root = filepath.Dir(root)
	}
	expected, err := n.OutputDir(root)
	if err != nil || clean != expected {
		return "", fmt.Errorf("%w: output path is not the bound namespace", ErrInvalidExecutorNamespace)
	}
	return root, nil
}

// claimExecutorOutput permanently consumes this executor name. Even after crash
// or cleanup a replacement must obtain a fresh binding, never reuse old files.
func claimExecutorOutput(output string, n ExecutorNamespaceV3) error {
	root, err := executorOutputRoot(output, n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	subdir, _ := n.OutputSubdir()
	parent := filepath.Dir(subdir)
	if err := directory.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	claim, err := directory.OpenFile(filepath.Join(parent, "."+n.ExecutorID+".claim"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExecutorReplacementRequired
		}
		return err
	}
	if err := claim.Close(); err != nil {
		return err
	}
	// Existing output, including a symlink, is never reused.
	if err := directory.Mkdir(subdir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExecutorReplacementRequired
		}
		return err
	}
	return nil
}

func (s *TranscodeSession) ExecutorNamespace() *ExecutorNamespaceV3 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneExecutorNamespace(s.opts.Executor)
}
func (s *TranscodeSession) CheckExecutorNamespace(expected *ExecutorNamespaceV3) error {
	return MatchExecutorNamespace(s.ExecutorNamespace(), expected)
}

// removeExecutorOutput removes only this executor's leaf. The consumed claim
// remains outside it, so stale cleanup cannot permit the name to be reused.
func removeExecutorOutput(output string, n ExecutorNamespaceV3) error {
	root, err := executorOutputRoot(output, n)
	if err != nil {
		return err
	}
	directory, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	subdir, _ := n.OutputSubdir()
	return directory.RemoveAll(subdir)
}
