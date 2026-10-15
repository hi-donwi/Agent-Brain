package router

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrModelCatalogPending distinguishes asynchronous catalog registration from
// authentication, identity, configuration and malformed-response errors.
var ErrModelCatalogPending = errors.New("the signed-in account's model catalog is not ready")

// WaitModels keeps the same bridge alive while its asynchronous account-specific
// model registration completes. It never substitutes a general model catalog.
func (b BridgeClient) WaitModels(ctx context.Context, provider, expected string, onWait func()) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return b.waitModels(ctx, provider, expected, 500*time.Millisecond, onWait)
}

func (b BridgeClient) waitModels(ctx context.Context, provider, expected string, interval time.Duration, onWait func()) ([]string, error) {
	notified := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrModelCatalogPending, err)
		}
		models, err := b.Models(ctx, provider, expected)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrModelCatalogPending, ctx.Err())
		}
		if !errors.Is(err, ErrModelCatalogPending) {
			return models, err
		}
		if !notified && onWait != nil {
			onWait()
			notified = true
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w; sign-in is retained, but no models were registered before the wait ended: %w", ErrModelCatalogPending, ctx.Err())
		case <-timer.C:
		}
	}
}
