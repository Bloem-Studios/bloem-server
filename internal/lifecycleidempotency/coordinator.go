package lifecycleidempotency

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Store interface {
	InTransaction(context.Context, func(context.Context, pgx.Tx) error) error
	LockHandoff(context.Context, pgx.Tx) error
	Phase(context.Context, pgx.Tx) (Phase, error)
	LockKey(context.Context, pgx.Tx, Digest) error
	Find(context.Context, pgx.Tx, Digest) (*Receipt, error)
	LockActor(context.Context, pgx.Tx, Binding) error
	Insert(context.Context, pgx.Tx, Receipt) error
	BindTargets(context.Context, pgx.Tx, Digest, TargetSource, []TargetBinding) error
	Complete(context.Context, pgx.Tx, Digest, Result) error
}

type KeyDigester func(string) Digest

type coordinator struct {
	store     Store
	digestKey KeyDigester
	responses cipher.AEAD
}

func NewCoordinator(store Store, digestKey KeyDigester) Coordinator {
	return &coordinator{store: store, digestKey: digestKey}
}

// NewEncryptedCoordinator protects retained response bodies at rest. Lifecycle
// receipts may contain newly issued access and refresh tokens, so production
// wiring must use this constructor rather than persisting their JSON plaintext.
func NewEncryptedCoordinator(store Store, secret []byte) Coordinator {
	keyMaterial := sha256.Sum256(append([]byte("bloem.lifecycle-response.v1\x00"), secret...))
	block, err := aes.NewCipher(keyMaterial[:])
	if err != nil {
		panic(fmt.Sprintf("create lifecycle response cipher: %v", err))
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(fmt.Sprintf("create lifecycle response AEAD: %v", err))
	}
	return &coordinator{store: store, digestKey: NewHMACKeyDigester(secret), responses: aead}
}

var encryptedResponsePrefix = []byte("bloem-lifecycle-response-v1\x00")

func NewHMACKeyDigester(secret []byte) KeyDigester {
	key := append([]byte(nil), secret...)
	return func(plaintext string) Digest {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("bloem.lifecycle-idempotency-key.v1\x00"))
		_, _ = mac.Write([]byte(plaintext))
		var digest Digest
		copy(digest[:], mac.Sum(nil))
		return digest
	}
}

func (c *coordinator) Execute(ctx context.Context, request Request, mutate Mutator) (Result, error) {
	if c.store == nil || c.digestKey == nil || mutate == nil {
		return Result{}, fmt.Errorf("lifecycle idempotency coordinator is not configured")
	}
	if request.IdempotencyKey != "" && !ValidKey(request.IdempotencyKey) {
		return Result{}, ErrKeyMalformed
	}
	if request.IdempotencyKey != "" && !validBinding(request.Binding) {
		return Result{}, ErrInvalidBinding
	}

	var result Result
	err := c.store.InTransaction(ctx, func(txCtx context.Context, tx pgx.Tx) error {
		if err := c.store.LockHandoff(txCtx, tx); err != nil {
			return err
		}
		phase, err := c.store.Phase(txCtx, tx)
		if err != nil {
			return err
		}
		if request.IdempotencyKey == "" {
			if phase == PhaseRequired {
				return ErrKeyRequired
			}
			if !validBinding(request.Binding) {
				return ErrInvalidBinding
			}
			if err := c.store.LockActor(txCtx, tx, request.Binding); err != nil {
				return err
			}
			binding, err := resolveBinding(txCtx, tx, request)
			if err != nil {
				return err
			}
			result, err = mutate(txCtx, tx, binding)
			return err
		}

		keyDigest := c.digestKey(request.IdempotencyKey)
		if err := c.store.LockKey(txCtx, tx, keyDigest); err != nil {
			return err
		}
		receipt, err := c.store.Find(txCtx, tx, keyDigest)
		if err != nil {
			return err
		}
		if receipt != nil {
			if !sameRequestBinding(receipt.Binding, request.Binding) {
				return ErrConflict
			}
			if receipt.State != StateCompleted {
				return ErrPending
			}
			result, err = c.replayResult(receipt.Result, keyDigest)
			if err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}

		if err := c.store.LockActor(txCtx, tx, request.Binding); err != nil {
			return err
		}
		binding, err := resolveBinding(txCtx, tx, request)
		if err != nil {
			return err
		}
		if err := c.store.Insert(txCtx, tx, Receipt{KeyDigest: keyDigest, Binding: binding, State: StateBound}); err != nil {
			return err
		}
		result, err = mutate(txCtx, tx, binding)
		if err != nil {
			return err
		}
		stored, err := c.storedResult(result, keyDigest)
		if err != nil {
			return err
		}
		if err := c.store.Complete(txCtx, tx, keyDigest, stored); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func (c *coordinator) ExecuteCreate(ctx context.Context, request Request, mutate CreateMutator) (Result, error) {
	if c.store == nil || c.digestKey == nil || mutate == nil {
		return Result{}, fmt.Errorf("lifecycle idempotency coordinator is not configured")
	}
	if request.IdempotencyKey != "" && !ValidKey(request.IdempotencyKey) {
		return Result{}, ErrKeyMalformed
	}
	if !validBinding(request.Binding) {
		return Result{}, ErrInvalidBinding
	}

	var result Result
	err := c.store.InTransaction(ctx, func(txCtx context.Context, tx pgx.Tx) error {
		if err := c.store.LockHandoff(txCtx, tx); err != nil {
			return err
		}
		phase, err := c.store.Phase(txCtx, tx)
		if err != nil {
			return err
		}
		if request.IdempotencyKey == "" {
			if phase == PhaseRequired {
				return ErrKeyRequired
			}
			if err := c.store.LockActor(txCtx, tx, request.Binding); err != nil {
				return err
			}
			_, result, err = mutate(txCtx, tx)
			return err
		}

		keyDigest := c.digestKey(request.IdempotencyKey)
		if err := c.store.LockKey(txCtx, tx, keyDigest); err != nil {
			return err
		}
		receipt, err := c.store.Find(txCtx, tx, keyDigest)
		if err != nil {
			return err
		}
		if receipt != nil {
			if !sameRequestBinding(receipt.Binding, request.Binding) {
				return ErrConflict
			}
			if receipt.State != StateCompleted {
				return ErrPending
			}
			result, err = c.replayResult(receipt.Result, keyDigest)
			if err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		if err := c.store.LockActor(txCtx, tx, request.Binding); err != nil {
			return err
		}
		unresolved := request.Binding
		unresolved.TargetSource = ""
		unresolved.TargetSetDigest = Digest{}
		unresolved.Targets = nil
		if err := c.store.Insert(txCtx, tx, Receipt{KeyDigest: keyDigest, Binding: unresolved, State: StateBindingUnresolved}); err != nil {
			return err
		}
		targets, createdResult, err := mutate(txCtx, tx)
		if err != nil {
			return err
		}
		targets = canonicalTargets(targets)
		if !validTargets(targets) {
			return ErrInvalidBinding
		}
		if err := c.store.BindTargets(txCtx, tx, keyDigest, request.Binding.TargetSource, targets); err != nil {
			return err
		}
		result = createdResult
		stored, err := c.storedResult(result, keyDigest)
		if err != nil {
			return err
		}
		return c.store.Complete(txCtx, tx, keyDigest, stored)
	})
	return result, err
}

func (c *coordinator) storedResult(result Result, keyDigest Digest) (Result, error) {
	stored := cloneResult(result)
	if c.responses == nil || len(stored.Body) == 0 {
		return stored, nil
	}
	nonce := make([]byte, c.responses.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Result{}, fmt.Errorf("generate lifecycle response nonce: %w", err)
	}
	sealed := c.responses.Seal(nil, nonce, stored.Body, keyDigest[:])
	stored.Body = make([]byte, 0, len(encryptedResponsePrefix)+len(nonce)+len(sealed))
	stored.Body = append(stored.Body, encryptedResponsePrefix...)
	stored.Body = append(stored.Body, nonce...)
	stored.Body = append(stored.Body, sealed...)
	return stored, nil
}

func (c *coordinator) replayResult(stored Result, keyDigest Digest) (Result, error) {
	result := cloneResult(stored)
	if c.responses == nil || len(result.Body) == 0 {
		return result, nil
	}
	if len(result.Body) < len(encryptedResponsePrefix) || !hmac.Equal(result.Body[:len(encryptedResponsePrefix)], encryptedResponsePrefix) {
		return Result{}, fmt.Errorf("lifecycle response is not encrypted")
	}
	payload := result.Body[len(encryptedResponsePrefix):]
	if len(payload) < c.responses.NonceSize() {
		return Result{}, fmt.Errorf("lifecycle response ciphertext is truncated")
	}
	nonce, ciphertext := payload[:c.responses.NonceSize()], payload[c.responses.NonceSize():]
	plaintext, err := c.responses.Open(nil, nonce, ciphertext, keyDigest[:])
	if err != nil {
		return Result{}, fmt.Errorf("decrypt lifecycle response: %w", err)
	}
	result.Body = plaintext
	return result, nil
}

func validBinding(binding Binding) bool {
	if binding.Method == "" || binding.RouteID == "" || binding.TargetSource == "" || binding.RequestHash == (Digest{}) {
		return false
	}
	switch binding.ActorKind {
	case ActorAuthenticatedAccount:
		return binding.ActorAccountID != nil && *binding.ActorAccountID > 0 &&
			binding.ActorAccountIncarnationID != nil && *binding.ActorAccountIncarnationID != uuid.Nil &&
			binding.ActorSubjectDigest == (Digest{})
	case ActorPreauthIntent:
		return binding.ActorAccountID == nil && binding.ActorAccountIncarnationID == nil &&
			binding.ActorSubjectDigest != (Digest{})
	default:
		return false
	}
}

func ValidKey(key string) bool {
	if len(key) < 16 || len(key) > 200 || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func resolveBinding(ctx context.Context, tx pgx.Tx, request Request) (Binding, error) {
	binding := request.Binding
	if request.ResolveTargets == nil {
		return Binding{}, fmt.Errorf("resolve lifecycle targets: resolver is required")
	}
	targets, err := request.ResolveTargets(ctx, tx)
	if err != nil {
		return Binding{}, err
	}
	binding.Targets = canonicalTargets(targets)
	if !validTargets(binding.Targets) {
		return Binding{}, ErrInvalidBinding
	}
	binding.TargetSetDigest = digestTargets(binding.Targets)
	return binding, nil
}

func validTargets(targets []TargetBinding) bool {
	if len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		if target.OrganizationID == uuid.Nil || target.MembershipID == uuid.Nil ||
			target.AccountID <= 0 || target.AccountIncarnationID == uuid.Nil {
			return false
		}
	}
	return true
}

func canonicalTargets(targets []TargetBinding) []TargetBinding {
	canonical := append([]TargetBinding(nil), targets...)
	sort.Slice(canonical, func(i, j int) bool {
		left, right := canonical[i], canonical[j]
		if left.OrganizationID != right.OrganizationID {
			return left.OrganizationID.String() < right.OrganizationID.String()
		}
		if left.MembershipID != right.MembershipID {
			return left.MembershipID.String() < right.MembershipID.String()
		}
		if left.AccountID != right.AccountID {
			return left.AccountID < right.AccountID
		}
		if left.AccountIncarnationID != right.AccountIncarnationID {
			return left.AccountIncarnationID.String() < right.AccountIncarnationID.String()
		}
		if left.ProfileID != right.ProfileID {
			return left.ProfileID < right.ProfileID
		}
		return left.ResourceID < right.ResourceID
	})
	return canonical
}

func sameRequestBinding(stored, incoming Binding) bool {
	if stored.ActorKind != incoming.ActorKind || stored.Method != incoming.Method ||
		stored.RouteID != incoming.RouteID || stored.RequestHash != incoming.RequestHash ||
		stored.TargetSource != incoming.TargetSource || stored.ActorSubjectDigest != incoming.ActorSubjectDigest {
		return false
	}
	if !sameInt(stored.ActorAccountID, incoming.ActorAccountID) ||
		!sameUUID(stored.ActorAccountIncarnationID, incoming.ActorAccountIncarnationID) {
		return false
	}
	return true
}

func sameInt(left, right *int) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func sameUUID(left, right *uuid.UUID) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func digestTargets(targets []TargetBinding) Digest {
	hash := sha256.New()
	_, _ = hash.Write([]byte("bloem.lifecycle-target-set.v1\x00"))
	var number [8]byte
	for _, target := range targets {
		_, _ = hash.Write(target.OrganizationID[:])
		_, _ = hash.Write(target.MembershipID[:])
		binary.BigEndian.PutUint64(number[:], uint64(target.AccountID))
		_, _ = hash.Write(number[:])
		_, _ = hash.Write(target.AccountIncarnationID[:])
		writeDigestString(hash, target.ProfileID, number[:])
		writeDigestString(hash, target.ResourceID, number[:])
	}
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest
}

type digestWriter interface{ Write([]byte) (int, error) }

func writeDigestString(writer digestWriter, value string, length []byte) {
	binary.BigEndian.PutUint64(length, uint64(len(value)))
	_, _ = writer.Write(length)
	_, _ = writer.Write([]byte(value))
}

func cloneResult(result Result) Result {
	result.Body = append([]byte(nil), result.Body...)
	if result.Headers != nil {
		result.Headers = make(map[string][]string, len(result.Headers))
		for key, values := range result.Headers {
			result.Headers[key] = append([]string(nil), values...)
		}
	}
	return result
}
