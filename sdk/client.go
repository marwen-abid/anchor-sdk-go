// Package sdk provides client-side integration with Stellar anchors.
// It handles SEP-10 authentication, session management, and endpoint discovery.
package sdk

import (
	"github.com/marwen-abid/anchor-sdk-go/core/net"
	"github.com/marwen-abid/anchor-sdk-go/core/toml"
)

// Client is the entry point for integrating with Stellar anchors.
// It discovers anchor endpoints via stellar.toml (SEP-1) and manages
// authentication sessions (SEP-10).
type Client struct {
	networkPassphrase string
	httpClient        *net.Client
	tomlResolver      *toml.Resolver
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets the underlying HTTP client for network requests.
func WithHTTPClient(client *net.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// NewClient creates a new Stellar Anchor client.
// The networkPassphrase identifies the Stellar network (e.g., "Test SDF Network ; September 2015").
func NewClient(networkPassphrase string, opts ...ClientOption) *Client {
	httpClient := net.NewClient()

	client := &Client{
		networkPassphrase: networkPassphrase,
		httpClient:        httpClient,
		tomlResolver:      toml.NewResolver(httpClient),
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}
