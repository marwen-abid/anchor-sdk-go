# Etherfuse Anchor Example

A complete Stellar anchor server that uses the [Etherfuse FX Ramp API](https://etherfuse.com) to enable **MXN ↔ Crypto** conversions on the Stellar network. Supports USDC and CETES (Mexican government treasury certificates) via SEP-24 interactive flows.

This example demonstrates how to build a production-shaped anchor using [anchor-sdk-go](../../), wiring together SEP-1 discovery, SEP-10 authentication, SEP-24 interactive deposits/withdrawals, webhook processing, and on-chain payment observation.

> **Status:** Proof-of-concept on Stellar testnet. Not production-ready.

## Quick Start

```bash
# 1. Get an Etherfuse sandbox API key from https://etherfuse.com
# 2. Configure
cp .env.example .env
# Edit .env and set ETHERFUSE_API_KEY

# 3. Run with Docker (recommended)
docker compose -f examples/anchor-etherfuse/docker-compose.yml up --build

# Or run directly (in-memory store, no PostgreSQL needed)
go run ./examples/anchor-etherfuse
```

The server starts on `http://localhost:8000` with these endpoints:

| Endpoint | Protocol |
|----------|----------|
| `/.well-known/stellar.toml` | SEP-1 Discovery |
| `/auth` | SEP-10 Authentication |
| `/sep24/info` | SEP-24 Asset Info |
| `/sep24/transactions/deposit/interactive` | SEP-24 Deposit |
| `/sep24/transactions/withdraw/interactive` | SEP-24 Withdrawal |

## Configuration

All settings are read from environment variables (or `.env` file). Env vars take precedence.

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `ETHERFUSE_API_KEY` | — | Yes | Etherfuse FX Ramp API key |
| `ETHERFUSE_API_URL` | `https://api.sand.etherfuse.com` | | Etherfuse API base URL |
| `ETHERFUSE_WEBHOOK_SECRET` | — | | HMAC secret for webhook verification |
| `ANCHOR_SECRET` | *(testnet key)* | | Stellar signing key (secret seed) |
| `ANCHOR_DOMAIN` | `localhost:8000` | | Domain for stellar.toml and JWT issuer |
| `ANCHOR_PORT` | `8000` | | HTTP listen port |
| `JWT_SECRET` | `test-jwt-secret-key-for-development` | | HMAC key for SEP-10 JWTs |
| `NETWORK_PASSPHRASE` | `Test SDF Network ; September 2015` | | Stellar network passphrase |
| `HORIZON_URL` | `https://horizon-testnet.stellar.org` | | Horizon API URL |
| `STORE_TYPE` | `memory` | | `memory` or `postgres` |
| `DATABASE_URL` | `postgres://anchor:anchor@localhost:5432/anchor?sslmode=disable` | | PostgreSQL connection string (when `STORE_TYPE=postgres`) |

### Persistent Storage (PostgreSQL)

By default (without Docker), all state is in-memory and lost on restart. The `docker compose up` command automatically uses PostgreSQL. To use PostgreSQL without Docker:

```bash
# Start only PostgreSQL
docker compose -f examples/anchor-etherfuse/docker-compose.yml up -d postgres

# Run the anchor with PostgreSQL store
STORE_TYPE=postgres go run ./examples/anchor-etherfuse
```

Migrations run automatically on startup (`CREATE TABLE IF NOT EXISTS`).

## Architecture

```
examples/anchor-etherfuse/
├── main.go           # Entry point, HTTP routing, component wiring
├── config.go         # Environment/dotenv configuration loading
├── etherfuse.go      # Etherfuse FX Ramp API client
├── sep24.go          # SEP-24 endpoints and response formatting
├── interactive.go    # Multi-step KYC/quote/order web UI handlers
├── webhooks.go       # Webhook processing + background order poller
├── doc.go            # Package documentation
├── templates/
│   └── interactive.html  # Single-page interactive flow template
├── docker-compose.yml    # PostgreSQL for optional persistence
├── .env.example          # Configuration template
└── .env                  # Local configuration (git-ignored)
```

### How It Fits Together

![Architecture overview showing wallet, anchor server components, Etherfuse API, Stellar network, and storage](docs/diagrams/architecture-overview.png)

The wallet only interacts with standard SEP endpoints. The `/interactive/*` pages are opened in a browser popup by the wallet and driven by the **user** — the wallet never calls those endpoints directly.

## Deposit Flow (MXN → Crypto)

A deposit converts fiat (MXN via SPEI bank transfer) into crypto (USDC or CETES) on Stellar.

![Deposit flow sequence diagram showing wallet, browser, anchor, Etherfuse, and Stellar interactions](docs/diagrams/deposit-flow.png)

### Deposit State Progression

![Deposit state progression from initiating through interactive, pending, to completed](docs/diagrams/deposit-states.png)

## Withdrawal Flow (Crypto → MXN)

A withdrawal converts crypto on Stellar into fiat (MXN sent to a bank account via SPEI).

![Withdrawal flow sequence diagram showing wallet, browser, anchor, Etherfuse, and Stellar interactions](docs/diagrams/withdrawal-flow.png)

### Withdrawal State Progression

![Withdrawal state progression from initiating through interactive, pending_external, to completed](docs/diagrams/withdrawal-states.png)

## HTTP Endpoints

### SEP Endpoints (Wallet Integration)

These follow the standard Stellar SEP protocols. Any compliant wallet can interact with them.

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/.well-known/stellar.toml` | — | SEP-1 discovery |
| GET | `/auth` | — | SEP-10 challenge |
| POST | `/auth` | — | SEP-10 verify → JWT |
| GET | `/sep24/info` | — | Asset info |
| POST | `/sep24/transactions/deposit/interactive` | JWT | Initiate deposit |
| POST | `/sep24/transactions/withdraw/interactive` | JWT | Initiate withdrawal |
| GET | `/sep24/transaction` | JWT | Single transfer status |
| GET | `/sep24/transactions` | JWT | List transfers |
| GET | `/transaction/{id}` | — | Transaction detail page |

### Interactive Flow (Browser Only)

These serve the anchor's web UI opened in a browser popup by the wallet. Users (not wallets) interact with these.

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/interactive` | Token | Route to current KYC/quote step |
| POST | `/interactive/onboard` | Token | Generate Etherfuse onboarding URL |
| GET | `/interactive/kyc-poll` | Token | AJAX KYC status polling |
| POST | `/interactive/quote` | Token | Create quote |
| POST | `/interactive/order` | Token | Create order (consumes token) |

### Webhooks

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| POST | `/webhooks/etherfuse` | HMAC-SHA256 | Etherfuse event notifications |

## Etherfuse Integration

### API Client

`etherfuse.go` wraps the Etherfuse FX Ramp API:

| Client Method | API Endpoint | Purpose |
|---------------|-------------|---------|
| `GetAssets()` | `GET /ramp/assets` | Discover available assets at startup |
| `GetKYCStatus()` | `GET /ramp/customer/{id}/kyc/{pk}` | Check user's KYC status |
| `GetOnboardingURL()` | `POST /ramp/onboarding-url` | Generate KYC onboarding link |
| `CreateQuote()` | `POST /ramp/quote` | Get exchange rate (2-min expiry) |
| `CreateOnrampOrder()` | `POST /ramp/order` | Create deposit order → CLABE |
| `CreateOfframpOrder()` | `POST /ramp/order` | Create withdrawal order → anchor account |
| `GetOrder()` | `GET /ramp/order/{id}` | Poll order status |

### Deterministic IDs

Same Stellar account always produces the same Etherfuse identifiers (UUID v5, SHA-1 based):

- `DeterministicCustomerID(stellarAccount)` — customer identity
- `DeterministicBankAccountID(stellarAccount)` — bank account
- `DeterministicQuoteID(transferID)` — quote
- `DeterministicOrderID(transferID)` — order

### Order Status Mapping

The order poller (every 10s) and webhook handler both drive the same state transitions via `applyOrderFields()`:

| Etherfuse Status | Deposit Transition | Withdrawal Transition |
|------------------|-------------------|-----------------------|
| `created` | — | Store withdraw account/memo |
| `funded` | `NotifyFundsReceived()` → pending_stellar | `NotifyPaymentReceived()` → pending_stellar |
| `completed` | `NotifyPaymentSent()` → completed | `NotifyDisbursementSent()` → completed |
| `failed` / `refunded` / `canceled` | `Cancel()` → cancelled | `Cancel()` → cancelled |

All transitions are idempotent — duplicate webhook/poll events are safely ignored.

## Transfer Metadata

Etherfuse-specific state is stored in the transfer's `Metadata` map under `etherfuse_*` keys:

| Key | Set When | Purpose |
|-----|----------|---------|
| `etherfuse_customer_id` | Onboarding | Etherfuse customer reference |
| `etherfuse_bank_account_id` | Onboarding | Bank account reference |
| `etherfuse_quote_id` | Quote | Quote reference |
| `etherfuse_exchange_rate` | Quote | Display exchange rate |
| `etherfuse_asset_code` | Quote | Selected asset (USDC/CETES) |
| `etherfuse_asset_id` | Quote | Full identifier (`CODE:ISSUER`) |
| `etherfuse_fee_amount` | Quote | Fee amount |
| `etherfuse_order_id` | Order | Links transfer to Etherfuse order |
| `etherfuse_deposit_clabe` | Deposit order | CLABE for user's SPEI transfer |
| `etherfuse_deposit_amount` | Deposit order | MXN amount to send |
| `etherfuse_withdraw_anchor_account` | Withdrawal order | Destination for user's Stellar payment |
| `etherfuse_withdraw_memo` | Withdrawal order | Payment memo |
| `etherfuse_withdraw_memo_type` | Withdrawal order | `text`, `id`, or `hash` |

## Background Services

### Horizon Observer

Streams Stellar payments to the distribution account. When a payment is detected, `AutoMatchPayments` extracts the memo (which is the transfer ID) and calls `NotifyPaymentReceived()` to advance the withdrawal FSM.

### Order Poller

Polls Etherfuse every 10 seconds for all non-terminal transfers. This is a fallback for local development where webhooks can't reach `localhost`. It drives the same `applyOrderFields()` logic as the webhook handler.

## Testing with a Wallet

Any SEP-24 compliant Stellar wallet can connect to this anchor. Point the wallet at `http://localhost:8000` as the home domain. The wallet will:

1. Fetch `/.well-known/stellar.toml` to discover endpoints
2. Authenticate via SEP-10 (`/auth`)
3. Initiate a deposit or withdrawal via SEP-24
4. Open the interactive URL in a browser popup for KYC/quote/order
5. Poll `/sep24/transaction` for status updates
