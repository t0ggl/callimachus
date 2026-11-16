package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Dispatcher coordinates multi-target notification delivery for media import events.
type Dispatcher struct {
	mu        sync.RWMutex
	notifiers []Notifier
	closed    bool
}

// NewDispatcher creates a new Dispatcher with the provided initial notifiers.
func NewDispatcher(notifiers ...Notifier) *Dispatcher {
	d := &Dispatcher{
		notifiers: make([]Notifier, 0, len(notifiers)),
	}
	for _, n := range notifiers {
		if n != nil {
			d.notifiers = append(d.notifiers, n)
		}
	}
	return d
}

// Register adds one or more notifiers to the dispatcher.
func (d *Dispatcher) Register(notifiers ...Notifier) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, n := range notifiers {
		if n != nil {
			d.notifiers = append(d.notifiers, n)
		}
	}
}

// Notifiers returns a copy of the currently registered notifiers.
func (d *Dispatcher) Notifiers() []Notifier {
	d.mu.RLock()
	defer d.mu.RUnlock()

	dst := make([]Notifier, len(d.notifiers))
	copy(dst, d.notifiers)
	return dst
}

// Dispatch fans out an ImportEvent to all registered notifiers concurrently.
// Individual notifier errors are collected and joined; one failure does not block others.
func (d *Dispatcher) Dispatch(ctx context.Context, event ImportEvent) error {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return ErrDispatcherClosed
	}
	notifiers := make([]Notifier, len(d.notifiers))
	copy(notifiers, d.notifiers)
	d.mu.RUnlock()

	if len(notifiers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, n := range notifiers {
		wg.Add(1)
		go func(notif Notifier) {
			defer wg.Done()
			if err := notif.NotifyImport(ctx, event); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", notif.Name(), err))
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()

	return errors.Join(errs...)
}

// Close gracefully closes all registered notifiers and returns any encountered errors.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	notifiers := make([]Notifier, len(d.notifiers))
	copy(notifiers, d.notifiers)
	d.mu.Unlock()

	var errs []error
	for _, n := range notifiers {
		if err := n.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s close: %w", n.Name(), err))
		}
	}

	return errors.Join(errs...)
}
