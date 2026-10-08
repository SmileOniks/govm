package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/gofrs/flock"
)

// Warning is a non-fatal condition discovered while recovering state.
// The mutation itself remains successful when only warnings are returned.
type Warning struct {
	Message string
	Err     error
}

func (w Warning) Error() string {
	if w.Err == nil {
		return w.Message
	}
	return fmt.Sprintf("%s: %v", w.Message, w.Err)
}

// RecoveryResult carries warnings produced by an operation-specific handler.
type RecoveryResult struct {
	Warnings []Warning
}

// RecoveryHandler completes or safely rolls back one operation's marker.
// The handler owns operation-specific phases, filesystem checks, and marker
// cleanup. It is called only after the coordinator has acquired the lock.
type RecoveryHandler interface {
	Recover(context.Context, Marker) (RecoveryResult, error)
}

// Mutation is executed while the global state mutation lock is held, after
// any existing transaction marker has been recovered.
type Mutation func(context.Context, *MarkerStore) error

// Result contains recovery warnings separately from the current mutation.
type Result struct {
	RecoveryWarnings []Warning
}

// BusyError reports fail-fast contention on the global mutation lock.
type BusyError struct {
	Path string
}

func (e *BusyError) Error() string {
	return "state mutation lock is busy"
}

// Coordinator serializes all installed-version state mutations.
type Coordinator struct {
	resolver *paths.Resolver
	mu       sync.RWMutex
	handlers map[Operation]RecoveryHandler
}

// NewCoordinator constructs a coordinator with no recovery handlers.
func NewCoordinator(resolver *paths.Resolver) *Coordinator {
	if resolver == nil {
		resolver = paths.New()
	}
	return &Coordinator{
		resolver: resolver,
		handlers: make(map[Operation]RecoveryHandler),
	}
}

// RegisterRecoveryHandler registers the handler for operation. An operation
// may have only one owner; duplicate registration fails rather than silently
// replacing the recovery policy while mutations may be running.
func (c *Coordinator) RegisterRecoveryHandler(operation Operation, handler RecoveryHandler) error {
	if !markerToken.MatchString(string(operation)) {
		return &MarkerError{Reason: "operation is empty or malformed"}
	}
	if handler == nil {
		return errors.New("recovery handler is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.handlers[operation]; exists {
		return fmt.Errorf("recovery handler for operation %q is already registered", operation)
	}
	c.handlers[operation] = handler
	return nil
}

// Mutate acquires the shared lock without waiting, recovers an existing
// transaction, checks cancellation, and invokes mutation under the lock.
func (c *Coordinator) Mutate(ctx context.Context, mutation Mutation) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if mutation == nil {
		return Result{}, errors.New("mutation is nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	root, err := c.resolver.RootDir()
	if err != nil {
		return Result{}, fmt.Errorf("resolve state root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Result{}, fmt.Errorf("create state root: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return Result{}, fmt.Errorf("inspect state root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return Result{}, fmt.Errorf("state root is not a real directory: %q", root)
	}
	lockPath, err := c.resolver.StateMutationLockFile()
	if err != nil {
		return Result{}, fmt.Errorf("resolve state mutation lock: %w", err)
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return Result{}, fmt.Errorf("acquire state mutation lock: %w", err)
	}
	if !locked {
		return Result{}, &BusyError{Path: lockPath}
	}
	defer func() { _ = lock.Unlock() }()

	markerPath, err := c.resolver.StateTransactionFile()
	if err != nil {
		return Result{}, fmt.Errorf("resolve state transaction marker: %w", err)
	}
	store := newMarkerStore(root, markerPath)
	marker, present, err := store.Read()
	if err != nil {
		return Result{}, err
	}

	var result Result
	if present {
		c.mu.RLock()
		handler, ok := c.handlers[marker.Operation]
		c.mu.RUnlock()
		if !ok {
			return Result{}, &MarkerError{Reason: fmt.Sprintf("no recovery handler for operation %q", marker.Operation)}
		}
		recovery, err := handler.Recover(ctx, marker)
		if err != nil {
			return Result{}, fmt.Errorf("recover %s transaction: %w", marker.Operation, err)
		}
		result.RecoveryWarnings = append(result.RecoveryWarnings, recovery.Warnings...)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := mutation(ctx, store); err != nil {
		return result, err
	}
	return result, nil
}
