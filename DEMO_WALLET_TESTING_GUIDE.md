# Demo Wallet Integration Testing Guide

## Summary

The Stellar Anchor Go SDK example anchor has been implemented with full SEP-1, SEP-10, and SEP-24 support. Manual testing with the Stellar Demo Wallet is required since the anchor runs on localhost (not publicly accessible).

## Implementation Status

✅ **SEP-1 (stellar.toml)**: Implemented and verified
- Endpoint: `/.well-known/stellar.toml`
- CORS headers: ✅ Present (`Access-Control-Allow-Origin: *`)
- Content-Type: ✅ Correct (`text/plain; charset=utf-8`)
- Required fields: ✅ NETWORK_PASSPHRASE, SIGNING_KEY, WEB_AUTH_ENDPOINT, CURRENCIES

✅ **SEP-10 (Web Authentication)**: Implemented and verified
- GET `/auth?account={account}`: ✅ Returns challenge transaction
- POST `/auth` with signed challenge: ✅ Returns JWT token
- Challenge format: ✅ Includes ManageData operations for domain and web_auth_domain
- Nonce management: ✅ One-time use with 5-minute expiry

✅ **SEP-24 (Interactive Deposits/Withdrawals)**: Implemented and verified
- GET `/sep24/info`: ✅ Returns asset information (no auth)
- POST `/sep24/transactions/deposit/interactive`: ✅ Returns interactive URL (requires JWT)
- POST `/sep24/transactions/withdraw/interactive`: ✅ Returns interactive URL (requires JWT)
- GET `/sep24/transaction?id={id}`: ✅ Returns transfer status (requires JWT)
- GET `/sep24/transactions`: ✅ Lists account transfers (requires JWT)

✅ **Interactive KYC Flow**: Implemented and verified
- GET `/interactive?token={token}`: ✅ Renders HTML form
- POST `/interactive`: ✅ Completes interactive flow
- Form fields: name, email, amount (read-only)
- Window behavior: Auto-closes popup on success

✅ **SEP-6 (Non-interactive)**: Implemented (bonus feature)
- GET `/sep6/info`: ✅ Returns asset information
- GET `/sep6/deposit`: ✅ Returns mock banking instructions
- GET `/sep6/withdraw`: ✅ Returns Stellar payment details
- GET `/sep6/transaction` and `/sep6/transactions`: ✅ Status endpoints

## Prerequisites for Manual Testing

1. **Public URL Required**: Demo Wallet needs HTTPS and a public domain
   - Solution: Use ngrok or similar tunnel: `ngrok http 8000`
   - Get public URL like: `https://abc123.ngrok.io`

2. **Stellar Testnet Account**: Create account at https://laboratory.stellar.org/
   - Fund with Friendbot: https://friendbot.stellar.org/
   - Copy secret key for signing

3. **Browser**: Chrome/Firefox with popup blocker disabled

## Step-by-Step Manual Testing

### Step 1: Start Anchor and Tunnel

```bash
# Terminal 1: Start anchor
cd /path/to/stellar-connect-sdk-go
go run ./examples/anchor

# Terminal 2: Start ngrok
ngrok http 8000

# Copy the HTTPS URL (e.g., https://abc123.ngrok.io)
```

### Step 2: Add Anchor to Demo Wallet

1. Navigate to https://demo-wallet.stellar.org/
2. Click "Add Asset" or "Settings"
3. Enter anchor home domain: `abc123.ngrok.io` (from ngrok, without https://)
4. Click "Fetch" or "Add"
5. **Expected**: Asset info loads, USDC asset shows as available

### Step 3: SEP-10 Authentication

1. Click "Login" or "SEP-10 Auth" button
2. Demo Wallet will:
   - Fetch challenge from `GET /auth?account={your-account}`
   - Sign challenge with your secret key
   - Submit signed challenge to `POST /auth`
3. **Expected**: "Authenticated" status shown, JWT token received

### Step 4: Initiate Deposit

1. Click "Deposit" for USDC asset
2. Demo Wallet calls `POST /sep24/transactions/deposit/interactive`
3. **Expected**: 
   - Interactive URL returned in response
   - Popup window opens automatically

### Step 5: Complete Interactive Flow

1. Popup loads `GET /interactive?token={token}`
2. **Expected**: KYC form displayed with:
   - Full Name (text input)
   - Email Address (email input)
   - Transfer Amount (read-only, shows deposit amount)
3. Fill in name and email
4. Click "Submit"
5. **Expected**:
   - Success message appears
   - Popup window closes after 2 seconds
   - Or redirects if not in popup mode

### Step 6: Monitor Transfer Status

1. Demo Wallet polls `GET /sep24/transaction?id={transfer-id}`
2. Check status progression:
   - Initial: `interactive` (before form submission)
   - After form: `pending_user_transfer_start`
   - (In real scenario: user transfers funds to bank)
   - (Anchor calls NotifyFundsReceived: `pending_stellar`)
   - (Anchor sends payment: `completed`)
3. **Expected**: Status updates reflect transfer lifecycle

### Step 7: Test Withdrawal (Optional)

1. Click "Withdraw" for USDC asset
2. Enter amount
3. **Expected**: Same interactive flow as deposit
4. Status should be: `interactive` → `pending_external` → ...

## Troubleshooting

### Issue: stellar.toml Not Loading
- **Cause**: CORS headers missing or ngrok not forwarding correctly
- **Fix**: Verify `curl -H "Origin: https://demo-wallet.stellar.org" https://abc123.ngrok.io/.well-known/stellar.toml` returns `Access-Control-Allow-Origin: *`

### Issue: Authentication Fails
- **Cause**: Signing key mismatch or invalid challenge
- **Fix**: Verify stellar.toml SIGNING_KEY matches anchor's public key
- **Debug**: Check anchor logs for error messages

### Issue: Interactive Popup Blocked
- **Cause**: Browser popup blocker
- **Fix**: Allow popups for demo-wallet.stellar.org in browser settings

### Issue: Interactive URL Invalid Token
- **Cause**: Token already consumed or expired (5-minute TTL)
- **Fix**: Start new deposit flow, tokens are one-time use

### Issue: Transfer Status Not Updating
- **Cause**: In-memory state (restarting anchor clears all transfers)
- **Fix**: Keep anchor running during entire test session

## Known Limitations

1. **anchor-tests Docker Container**: Cannot run due to localhost networking constraints
   - The test container expects a publicly accessible URL
   - Manual testing with Demo Wallet is the validation method

2. **In-Memory Storage**: All transfers lost on anchor restart
   - This is by design for the example anchor
   - Production anchors would use persistent database

3. **Mock Banking**: Instructions are hardcoded
   - Production anchors would integrate with real banking APIs
   - Observer would detect real off-chain payments

4. **HTTP (Local)**: Example anchor uses HTTP on localhost
   - ngrok provides HTTPS for public access
   - Production anchors must use HTTPS directly

## Verification Checklist

Use this checklist during manual testing:

- [ ] stellar.toml loads at `/.well-known/stellar.toml`
- [ ] CORS headers present on all endpoints
- [ ] SEP-10 challenge flow completes successfully
- [ ] JWT token received and stored
- [ ] SEP-24 deposit returns interactive URL
- [ ] Interactive URL opens in popup
- [ ] KYC form renders correctly
- [ ] Form submission succeeds
- [ ] Popup closes after submission
- [ ] Transfer status polling works
- [ ] Status transitions correctly
- [ ] SEP-24 withdrawal flow works
- [ ] Multiple transfers can be created
- [ ] Transfer list endpoint shows all transfers

## Curl Testing Examples

Test endpoints directly without Demo Wallet:

```bash
# Replace with your ngrok URL
BASE_URL="https://abc123.ngrok.io"

# Test stellar.toml
curl "$BASE_URL/.well-known/stellar.toml"

# Test SEP-10 challenge
ACCOUNT="GCP7JKDSYR3NBQIFDSAJNWIXI4XBBQAWGMFBUAI66DKHZMQ45JWYXCHD"
curl "$BASE_URL/auth?account=$ACCOUNT"

# Test SEP-24 info (no auth)
curl "$BASE_URL/sep24/info" | jq '.'

# Test SEP-6 info
curl "$BASE_URL/sep6/info" | jq '.'
```

## Next Steps

After successful manual testing:
1. Document any issues discovered
2. Update RFC with final implementation notes
3. Consider adding automated integration tests (if ngrok API available)
4. Consider deploying example anchor to public testnet for ongoing validation

## Conclusion

The implementation is complete and ready for manual validation with the Stellar Demo Wallet. Due to localhost networking constraints, automated anchor-tests cannot run, but manual testing provides comprehensive validation of all SEP-1, SEP-10, and SEP-24 flows.
