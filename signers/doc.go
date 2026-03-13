// Package signers provides convenience constructors for creating Signer implementations.
//
// It offers two patterns:
//   - FromSecret: Wraps a Stellar secret key (S...) using stellar/go keypair for signing.
//     Intended for server-side use (exchanges, backends, bots).
//   - FromCallback: Wraps a custom signing function (e.g., HSM, custodial API, external service).
//     Allows you to delegate signing to any external infrastructure.
//
// Both return implementations of the anchorsdk.Signer interface.
package signers
