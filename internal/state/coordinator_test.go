package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/gofrs/flock"
)

type testRecoveryHandler struct {
	called bool
	marker Marker
	result RecoveryResult
	err    error
}

func (h *testRecoveryHandler) Recover(_ context.Context, marker Marker) (RecoveryResult, error) {
	h.called = true
	h.marker = marker
	return h.result, h.err
}

func testResolver(t *testing.T) (*paths.Resolver, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".govm")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return &paths.Resolver{
		HomeDir: func() (string, error) { return home, nil },
	}, root
}

func TestCoordinatorMutateDispatchesRecoveryAndRunsUnderLock(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	store := NewMarkerStore(root)
	marker := validMarker()
	if err := store.Write(marker); err != nil {
		t.Fatal(err)
	}

	handler := &testRecoveryHandler{
		result: RecoveryResult{Warnings: []Warning{{Message: "recovered warning"}}},
	}
	coordinator := NewCoordinator(resolver)
	if err := coordinator.RegisterRecoveryHandler(marker.Operation, handler); err != nil {
		t.Fatal(err)
	}

	called := false
	result, err := coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
		called = true
		lockPath, err := resolver.StateMutationLockFile()
		if err != nil {
			return err
		}
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil {
			return err
		}
		if locked {
			_ = lock.Unlock()
			t.Fatal("mutation callback did not run under the shared lock")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}
	if !called {
		t.Fatal("mutation callback was not called")
	}
	if !handler.called || !reflect.DeepEqual(handler.marker, marker) {
		t.Fatalf("handler called = %t, marker = %#v, want %#v", handler.called, handler.marker, marker)
	}
	if len(result.RecoveryWarnings) != 1 || result.RecoveryWarnings[0].Message != "recovered warning" {
		t.Fatalf("RecoveryWarnings = %#v", result.RecoveryWarnings)
	}
}

func TestCoordinatorMissingMarkerSkipsRecovery(t *testing.T) {
	t.Parallel()

	resolver, _ := testResolver(t)
	coordinator := NewCoordinator(resolver)
	called := false
	if _, err := coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}
	if !called {
		t.Fatal("mutation callback was not called")
	}
}

func TestCoordinatorUnknownOperationFailsClosed(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	store := NewMarkerStore(root)
	marker := validMarker()
	marker.Operation = Operation("future")
	if err := store.Write(marker); err != nil {
		t.Fatal(err)
	}

	coordinator := NewCoordinator(resolver)
	called := false
	if _, err := coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("Mutate() error = nil, want unknown operation error")
	}
	if called {
		t.Fatal("mutation callback ran despite unknown marker operation")
	}
}

func TestCoordinatorRecoveryFailureFailsClosed(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	store := NewMarkerStore(root)
	if err := store.Write(validMarker()); err != nil {
		t.Fatal(err)
	}
	handler := &testRecoveryHandler{err: errors.New("recovery failed")}
	coordinator := NewCoordinator(resolver)
	if err := coordinator.RegisterRecoveryHandler(OperationActivate, handler); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("Mutate() error = nil, want recovery failure")
	}
	if called {
		t.Fatal("mutation callback ran despite recovery failure")
	}
}

func TestCoordinatorCancellationBeforeCallback(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	if err := NewMarkerStore(root).Write(validMarker()); err != nil {
		t.Fatal(err)
	}
	handler := &testRecoveryHandler{}
	coordinator := NewCoordinator(resolver)
	if err := coordinator.RegisterRecoveryHandler(OperationActivate, handler); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if _, err := coordinator.Mutate(ctx, func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Mutate() error = %v, want context.Canceled", err)
	}
	if called || handler.called {
		t.Fatal("callbacks ran for an already-canceled context")
	}
}

func TestCoordinatorCancellationAfterRecoverySkipsMutation(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	if err := NewMarkerStore(root).Write(validMarker()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	handler := &testRecoveryHandler{}
	handler.result = RecoveryResult{}
	coordinator := NewCoordinator(resolver)
	if err := coordinator.RegisterRecoveryHandler(OperationActivate, recoveryCanceler{cancel: cancel, handler: handler}); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := coordinator.Mutate(ctx, func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Mutate() error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("mutation callback ran after recovery canceled context")
	}
}

type recoveryCanceler struct {
	cancel  context.CancelFunc
	handler *testRecoveryHandler
}

func (h recoveryCanceler) Recover(ctx context.Context, marker Marker) (RecoveryResult, error) {
	result, err := h.handler.Recover(ctx, marker)
	h.cancel()
	return result, err
}

func TestCoordinatorBusyFailsFast(t *testing.T) {
	t.Parallel()

	resolver, root := testResolver(t)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath, err := resolver.StateMutationLockFile()
	if err != nil {
		t.Fatal(err)
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("test could not acquire lock")
	}
	defer func() { _ = lock.Unlock() }()

	coordinator := NewCoordinator(resolver)
	_, err = coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
		return nil
	})
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("Mutate() error = %v, want *BusyError", err)
	}
	if busy.Path != lockPath {
		t.Fatalf("BusyError.Path = %q, want %q", busy.Path, lockPath)
	}
}

func TestCoordinatorRejectsDuplicateRecoveryHandler(t *testing.T) {
	t.Parallel()

	resolver, _ := testResolver(t)
	coordinator := NewCoordinator(resolver)
	first := &testRecoveryHandler{}
	if err := coordinator.RegisterRecoveryHandler(OperationInstall, first); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RegisterRecoveryHandler(OperationInstall, &testRecoveryHandler{}); err == nil {
		t.Fatal("duplicate RegisterRecoveryHandler() error = nil")
	}
}

func TestCoordinatorConcurrentRegistrationAndMutation(t *testing.T) {
	t.Parallel()

	resolver, _ := testResolver(t)
	coordinator := NewCoordinator(resolver)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			_ = coordinator.RegisterRecoveryHandler(
				Operation("test-"+string(rune('a'+index))),
				&testRecoveryHandler{},
			)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = coordinator.Mutate(context.Background(), func(context.Context, *MarkerStore) error {
				return nil
			})
		}()
	}
	wg.Wait()
}

func TestCoordinatorRejectsSymlinkStateRoot(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".govm")); err != nil {
		t.Fatal(err)
	}
	resolver := &paths.Resolver{HomeDir: func() (string, error) { return home, nil }}
	coordinator := NewCoordinator(resolver)
	called := false
	if _, err := coordinator.Mutate(t.Context(), func(context.Context, *MarkerStore) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("Mutate() error = nil for symlink state root")
	}
	if called {
		t.Fatal("mutation ran for symlink state root")
	}
}
