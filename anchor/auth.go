package anchor

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/marwen-abid/anchor-sdk-go"
	corecrypto "github.com/marwen-abid/anchor-sdk-go/core/crypto"
	"github.com/marwen-abid/anchor-sdk-go/errors"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
)

const (
	challengeNonceLength = 48
	challengeTimeout     = 5 * time.Minute
	challengeBaseFee     = int64(100)
	authMethodWebAuth    = "web_auth"
)

type authClaimsContextKey struct{}

var claimsContextKey = authClaimsContextKey{}

type AuthConfig struct {
	Domain            string
	NetworkPassphrase string
	Signer            anchorsdk.Signer
	NonceStore        anchorsdk.NonceStore
	JWTIssuer         anchorsdk.JWTIssuer
	JWTVerifier       anchorsdk.JWTVerifier
	AccountFetcher    anchorsdk.AccountFetcher // Optional: enables account signer support
}

type AuthIssuer struct {
	domain            string
	networkPassphrase string
	signer            anchorsdk.Signer
	nonceStore        anchorsdk.NonceStore
	jwtIssuer         anchorsdk.JWTIssuer
	jwtVerifier       anchorsdk.JWTVerifier
	accountFetcher    anchorsdk.AccountFetcher
}

func NewAuthIssuer(config AuthConfig) (*AuthIssuer, error) {
	if strings.TrimSpace(config.Domain) == "" {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "domain is required", nil)
	}
	if strings.TrimSpace(config.NetworkPassphrase) == "" {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "network passphrase is required", nil)
	}
	if config.Signer == nil {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "signer is required", nil)
	}
	if config.NonceStore == nil {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "nonce store is required", nil)
	}
	if config.JWTIssuer == nil {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "JWT issuer is required", nil)
	}
	if config.JWTVerifier == nil {
		return nil, errors.NewAnchorError(errors.CONFIG_INVALID, "JWT verifier is required", nil)
	}

	return &AuthIssuer{
		domain:            config.Domain,
		networkPassphrase: config.NetworkPassphrase,
		signer:            config.Signer,
		nonceStore:        config.NonceStore,
		jwtIssuer:         config.JWTIssuer,
		jwtVerifier:       config.JWTVerifier,
		accountFetcher:    config.AccountFetcher,
	}, nil
}

func (a *AuthIssuer) CreateChallenge(ctx context.Context, account string) (string, error) {
	if strings.TrimSpace(account) == "" {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "account is required", nil)
	}

	if _, err := keypair.ParseAddress(account); err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "invalid account address", err)
	}

	nonce, err := corecrypto.GenerateNonce(challengeNonceLength)
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "failed to generate nonce", err)
	}

	expiresAt := time.Now().Add(challengeTimeout)
	if err := a.nonceStore.Add(ctx, nonce, expiresAt); err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "failed to store nonce", err)
	}

	now := time.Now().UTC()
	maxTime := now.Add(challengeTimeout)
	serverAccount := a.signer.PublicKey()
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: serverAccount, Sequence: 0},
		IncrementSequenceNum: false,
		Operations: []txnbuild.Operation{
			&txnbuild.ManageData{Name: a.domain + " auth", Value: []byte(nonce), SourceAccount: account},
			&txnbuild.ManageData{Name: "web_auth_domain", Value: []byte(a.domain), SourceAccount: serverAccount},
		},
		BaseFee: challengeBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimebounds(now.Unix(), maxTime.Unix()),
		},
	})
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "failed to build challenge transaction", err)
	}

	xdr, err := tx.Base64()
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "failed to encode challenge transaction", err)
	}

	signedXDR, err := a.signer.SignTransaction(ctx, xdr, a.networkPassphrase)
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_BUILD_FAILED, "failed to sign challenge transaction", err)
	}

	return signedXDR, nil
}

func (a *AuthIssuer) VerifyChallenge(ctx context.Context, challengeXDR string) (string, error) {
	if strings.TrimSpace(challengeXDR) == "" {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge XDR is required", nil)
	}

	parsed, err := txnbuild.TransactionFromXDR(challengeXDR)
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "failed to parse challenge transaction", err)
	}

	tx, ok := parsed.Transaction()
	if !ok {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction must not be fee bump", nil)
	}

	operations := tx.Operations()
	if len(operations) < 2 {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction must have at least two operations", nil)
	}

	firstOp, ok := operations[0].(*txnbuild.ManageData)
	if !ok {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "first operation must be manage_data", nil)
	}
	if firstOp.Value == nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge nonce missing", nil)
	}
	if firstOp.Name != a.domain+" auth" {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "invalid challenge operation name", nil)
	}

	nonce := string(firstOp.Value)
	consumed, err := a.nonceStore.Consume(ctx, nonce)
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "failed to consume nonce", err)
	}
	if !consumed {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "nonce already used or expired", nil)
	}

	// Verify transaction source account is the server
	txSourceAccount := tx.SourceAccount().AccountID
	if txSourceAccount != a.signer.PublicKey() {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction source account must be the server signing key", nil)
	}

	// Extract client account from first operation's SourceAccount (per SEP-10)
	account := firstOp.SourceAccount
	if strings.TrimSpace(account) == "" {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "first operation missing source account (client account)", nil)
	}
	if err := verifyChallengeSignatures(ctx, tx, a.networkPassphrase, a.signer.PublicKey(), account, a.accountFetcher); err != nil {
		return "", err
	}

	secondOp, ok := operations[1].(*txnbuild.ManageData)
	if !ok {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "second operation must be manage_data", nil)
	}
	if secondOp.Name != "web_auth_domain" {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "web_auth_domain operation missing", nil)
	}
	if !bytes.Equal(secondOp.Value, []byte(a.domain)) {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "web_auth_domain value mismatch", nil)
	}

	claims := anchorsdk.JWTClaims{
		Subject:    account,
		Issuer:     a.domain,
		AuthMethod: authMethodWebAuth,
	}
	token, err := a.jwtIssuer.Issue(ctx, claims)
	if err != nil {
		return "", errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "failed to issue JWT", err)
	}

	return token, nil
}

func (a *AuthIssuer) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.jwtVerifier == nil {
			http.Error(w, "auth verifier not configured", http.StatusInternalServerError)
			return
		}
		if next == nil {
			http.Error(w, "handler not configured", http.StatusInternalServerError)
			return
		}

		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"missing bearer token"}`))
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"missing bearer token"}`))
			return
		}

		claims, err := a.jwtVerifier.Verify(r.Context(), token)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"invalid token"}`))
			return
		}

		ctx := context.WithValue(r.Context(), claimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ClaimsFromContext(ctx context.Context) (*anchorsdk.JWTClaims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*anchorsdk.JWTClaims)
	return claims, ok
}

func verifyChallengeSignatures(ctx context.Context, tx *txnbuild.Transaction, networkPassphrase, serverPublicKey, clientAccount string, fetcher anchorsdk.AccountFetcher) error {
	serverKP, err := keypair.ParseAddress(serverPublicKey)
	if err != nil {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "invalid server public key", err)
	}

	// Build the set of valid client signers and determine the threshold.
	// If an AccountFetcher is provided, look up account signers from the network.
	// Otherwise (or if the account is unfunded), fall back to master-key-only.
	type clientSigner struct {
		kp     keypair.KP
		weight int32
	}
	var clientSigners []clientSigner
	var medThreshold int32

	if fetcher != nil {
		signers, thresholds, fetchErr := fetcher.FetchSigners(ctx, clientAccount)
		if fetchErr != nil {
			// Account not found (unfunded) — per SEP-10, fall back to master key only
			kp, err := keypair.ParseAddress(clientAccount)
			if err != nil {
				return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "invalid account address", err)
			}
			clientSigners = []clientSigner{{kp: kp, weight: 1}}
			medThreshold = 0
		} else {
			medThreshold = int32(thresholds.Medium)
			clientSigners = make([]clientSigner, 0, len(signers))
			for _, s := range signers {
				kp, err := keypair.ParseAddress(s.Key)
				if err != nil {
					continue // skip invalid signer keys
				}
				clientSigners = append(clientSigners, clientSigner{kp: kp, weight: s.Weight})
			}
		}
	} else {
		// No fetcher — legacy behavior: master key only, threshold 0
		kp, err := keypair.ParseAddress(clientAccount)
		if err != nil {
			return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "invalid account address", err)
		}
		clientSigners = []clientSigner{{kp: kp, weight: 1}}
		medThreshold = 0
	}

	sigs := tx.Signatures()
	if len(sigs) == 0 {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction has no signatures", nil)
	}

	hash, err := tx.Hash(networkPassphrase)
	if err != nil {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "failed to hash challenge transaction", err)
	}

	serverSigned := false
	var totalWeight int32
	seenHints := make(map[[4]byte]bool)

	for _, sig := range sigs {
		// Reject duplicate signature hints
		var hint [4]byte
		copy(hint[:], sig.Hint[:])
		if seenHints[hint] {
			return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "duplicate signature detected", nil)
		}
		seenHints[hint] = true

		// Check if this is the server's signature
		if serverKP.Verify(hash[:], sig.Signature) == nil {
			serverSigned = true
			continue
		}

		// Check against registered client signers
		matched := false
		for _, cs := range clientSigners {
			if cs.kp.Verify(hash[:], sig.Signature) == nil {
				totalWeight += cs.weight
				matched = true
				break
			}
		}
		if !matched {
			return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "transaction has unrecognized signatures", nil)
		}
	}

	if !serverSigned {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction not signed by server", nil)
	}
	if totalWeight < medThreshold {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction not signed by client", nil)
	}
	// For unfunded accounts (medThreshold == 0), we still need at least one client signature
	if medThreshold == 0 && totalWeight == 0 {
		return errors.NewAnchorError(errors.CHALLENGE_VERIFY_FAILED, "challenge transaction not signed by client", nil)
	}

	return nil
}
