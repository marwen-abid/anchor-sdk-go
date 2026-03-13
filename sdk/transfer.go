package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	anchorsdk "github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// TransferProcess represents an in-progress deposit or withdrawal.
// It manages client-side status polling with adaptive backoff and event emission.
type TransferProcess struct {
	ID             string
	Status         anchorsdk.TransferStatus
	InteractiveURL string

	onStatusChange func(anchorsdk.TransferStatus)
	onInteractive  func(string)

	session  *Session
	endpoint string
}

// OnStatusChange registers a callback invoked when the transfer status changes.
// The handler receives the new status value.
func (t *TransferProcess) OnStatusChange(handler func(anchorsdk.TransferStatus)) {
	t.onStatusChange = handler
}

// OnInteractive registers a callback invoked when an interactive URL becomes available.
// This is the wallet's hook for opening a popup or browser redirect.
func (t *TransferProcess) OnInteractive(handler func(string)) {
	t.onInteractive = handler
	if t.InteractiveURL != "" {
		handler(t.InteractiveURL)
	}
}

// Poll fetches the current transfer status from the anchor.
// It updates the TransferProcess.Status field and invokes the onStatusChange callback
// if the status has changed.
func (t *TransferProcess) Poll(ctx context.Context) error {
	url := fmt.Sprintf("%s/transaction?id=%s", t.endpoint, t.ID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return errors.NewClientError(
			errors.TRANSFER_STATUS_POLL_FAILED,
			"failed to create poll request",
			err,
		)
	}

	req.Header.Set("Authorization", "Bearer "+t.session.JWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.NewClientError(
			errors.TRANSFER_STATUS_POLL_FAILED,
			"failed to poll transfer "+t.ID,
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return errors.NewClientError(
			errors.TRANSFER_STATUS_POLL_FAILED,
			fmt.Sprintf("poll request returned status %d: %s", resp.StatusCode, string(body)),
			nil,
		)
	}

	var pollResp struct {
		Transaction struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"transaction"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return errors.NewClientError(
			errors.TRANSFER_STATUS_POLL_FAILED,
			"failed to decode poll response JSON",
			err,
		)
	}

	oldStatus := t.Status
	newStatus := anchorsdk.TransferStatus(pollResp.Transaction.Status)

	if newStatus != oldStatus {
		t.Status = newStatus
		if t.onStatusChange != nil {
			t.onStatusChange(newStatus)
		}
	}

	return nil
}

// WaitForCompletion blocks until the transfer reaches a terminal status.
// It polls the anchor using adaptive backoff: 1s, 2s, 4s, 8s, max 30s.
// Returns when the transfer completes, fails, or the context is cancelled.
func (t *TransferProcess) WaitForCompletion(ctx context.Context) error {
	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		if err := t.Poll(ctx); err != nil {
			return err
		}

		if t.isTerminal() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (t *TransferProcess) isTerminal() bool {
	switch t.Status {
	case anchorsdk.StatusCompleted,
		anchorsdk.StatusFailed,
		anchorsdk.StatusDenied,
		anchorsdk.StatusCancelled,
		anchorsdk.StatusExpired:
		return true
	default:
		return false
	}
}
