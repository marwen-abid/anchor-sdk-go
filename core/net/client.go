// Package net provides HTTP client functionality with retry, timeout, and circuit breaker patterns
// for making requests to Stellar services (Horizon, anchor servers).
//
// The Client struct offers configurable timeout, retry attempts, and exponential backoff.
// It includes a simple circuit breaker to prevent cascading failures when services are down.
//
// Example usage:
//
//	client := net.NewClient(
//	    net.WithTimeout(20*time.Second),
//	    net.WithMaxRetries(5),
//	    net.WithRetryBackoff(2*time.Second),
//	)
//	resp, err := client.Get(ctx, "https://horizon.stellar.org/...")
package net

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// Default configuration values
const (
	defaultTimeout      = 30 * time.Second
	defaultMaxRetries   = 3
	defaultBackoff      = 1 * time.Second
	defaultFailureLimit = 5
	defaultResetTimeout = 60 * time.Second
)

// Client is an HTTP client with retry, timeout, and circuit breaker capabilities.
type Client struct {
	httpClient     *http.Client
	maxRetries     int
	retryBackoff   time.Duration
	circuitBreaker *circuitBreaker
}

// ClientOption is a function that configures a Client.
type ClientOption func(*Client)

// WithTimeout sets the HTTP client timeout (default: 30s).
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// WithMaxRetries sets the maximum number of retry attempts (default: 3).
func WithMaxRetries(n int) ClientOption {
	return func(c *Client) {
		c.maxRetries = n
	}
}

// WithRetryBackoff sets the base duration for exponential backoff (default: 1s).
func WithRetryBackoff(d time.Duration) ClientOption {
	return func(c *Client) {
		c.retryBackoff = d
	}
}

// NewClient creates a new HTTP client with the given options.
func NewClient(opts ...ClientOption) *Client {
	client := &Client{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		maxRetries:   defaultMaxRetries,
		retryBackoff: defaultBackoff,
		circuitBreaker: &circuitBreaker{
			failureLimit: defaultFailureLimit,
			resetTimeout: defaultResetTimeout,
		},
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// Response wraps an HTTP response with convenience methods.
type Response struct {
	*http.Response
}

// Get performs an HTTP GET request with retry and circuit breaker logic.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, errors.NewCoreError(errors.NETWORK_ERROR, "failed to create GET request", err)
	}
	return c.do(req)
}

// Post performs an HTTP POST request with retry and circuit breaker logic.
func (c *Client) Post(ctx context.Context, rawURL string, body io.Reader) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, body)
	if err != nil {
		return nil, errors.NewCoreError(errors.NETWORK_ERROR, "failed to create POST request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// PostForm performs an HTTP POST request with form data.
func (c *Client) PostForm(ctx context.Context, urlStr string, data url.Values) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.NewCoreError(errors.NETWORK_ERROR, "failed to create POST form request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// do executes the HTTP request with retry logic and circuit breaker.
func (c *Client) do(req *http.Request) (*Response, error) {
	// Check circuit breaker
	if !c.circuitBreaker.allowRequest() {
		return nil, errors.NewCoreError(
			errors.NETWORK_ERROR,
			"circuit breaker is open",
			nil,
		)
	}

	// Buffer the request body so it can be replayed on retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, errors.NewCoreError(errors.NETWORK_ERROR, "failed to read request body", err)
		}
		_ = req.Body.Close()
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Check context cancellation
		select {
		case <-req.Context().Done():
			return nil, errors.NewCoreError(
				errors.NETWORK_ERROR,
				"request cancelled",
				req.Context().Err(),
			)
		default:
		}

		// Reset body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			// Network error - retry
			if attempt < c.maxRetries {
				c.backoff(attempt)
				continue
			}
			c.circuitBreaker.recordFailure()
			return nil, errors.NewCoreError(
				errors.NETWORK_ERROR,
				fmt.Sprintf("request failed after %d attempts", attempt+1),
				err,
			)
		}

		// Check status code
		if resp.StatusCode >= 500 {
			// Server error - retry
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("server error: %d %s", resp.StatusCode, resp.Status)
			if attempt < c.maxRetries {
				c.backoff(attempt)
				continue
			}
			c.circuitBreaker.recordFailure()
			return nil, errors.NewCoreError(
				errors.NETWORK_ERROR,
				fmt.Sprintf("server error after %d attempts: %s", attempt+1, resp.Status),
				lastErr,
			)
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Client error - don't retry
			c.circuitBreaker.recordSuccess()
			return &Response{resp}, nil
		}

		// Success
		c.circuitBreaker.recordSuccess()
		return &Response{resp}, nil
	}

	// Should not reach here
	return nil, errors.NewCoreError(
		errors.NETWORK_ERROR,
		"unexpected retry exhaustion",
		lastErr,
	)
}

// backoff implements exponential backoff with the formula: backoff * 2^attempt
func (c *Client) backoff(attempt int) {
	duration := c.retryBackoff * (1 << uint(attempt)) // 2^attempt
	time.Sleep(duration)
}

// circuitBreaker implements a simple circuit breaker pattern.
type circuitBreaker struct {
	mu           sync.RWMutex
	failures     int
	lastFailTime time.Time
	failureLimit int
	resetTimeout time.Duration
	state        circuitState
}

type circuitState int

const (
	stateClosed circuitState = iota
	stateOpen
)

// allowRequest checks if the circuit breaker allows the request to proceed.
func (cb *circuitBreaker) allowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.state == stateClosed {
		return true
	}

	// Check if reset timeout has elapsed
	if time.Since(cb.lastFailTime) > cb.resetTimeout {
		return true
	}

	return false
}

// recordSuccess records a successful request and may close the circuit.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = stateClosed
}

// recordFailure records a failed request and may open the circuit.
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailTime = time.Now()

	if cb.failures >= cb.failureLimit {
		cb.state = stateOpen
	}
}
