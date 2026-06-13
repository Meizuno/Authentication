// Package signing encapsulates access-token signing and verification. It
// supports a configurable active algorithm (HS256, EdDSA, RS256) for signing
// while always being able to verify asymmetric tokens, and — as long as a
// legacy HMAC secret is present — legacy HS256 tokens. Key selection during
// verification is strict per token method, which defeats the RS->HS algorithm
// confusion attack.
package signing

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/golang-jwt/jwt/v5"
)

// Algorithm names accepted in configuration.
const (
	AlgHS256 = "HS256"
	AlgEdDSA = "EdDSA"
	AlgRS256 = "RS256"
)

// Signer signs access tokens with the configured active algorithm and verifies
// presented tokens with strict per-method key selection.
type Signer struct {
	activeAlg    string
	signMethod   jwt.SigningMethod
	signKey      any // []byte (HS256) or a crypto.Signer (asymmetric)
	legacySecret []byte

	// Asymmetric material, populated whenever a private key is configured —
	// even under HS256, so the public key can be published ahead of the cutover.
	publicKey crypto.PublicKey
	kid       string
	jwk       *jwk
}

// NewSigner builds a Signer. A private key (PEM) is parsed whenever provided so
// its public half can be published via JWKS; the active alg decides what is
// actually used to sign. Returns an error for an unparseable key, an asymmetric
// alg without a matching key, or HS256 with a too-short secret.
func NewSigner(alg, privateKeyPEM string, secret []byte) (*Signer, error) {
	if alg == "" {
		alg = AlgHS256
	}
	s := &Signer{activeAlg: alg, legacySecret: secret}

	var priv crypto.Signer
	if privateKeyPEM != "" {
		var err error
		priv, err = parsePrivateKey(privateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		s.publicKey = priv.Public()
		kid, j, err := publicJWK(s.publicKey)
		if err != nil {
			return nil, err
		}
		s.kid, s.jwk = kid, j
	}

	switch alg {
	case AlgHS256:
		if len(secret) < 32 {
			return nil, errors.New("HS256 requires JWT_SECRET of at least 32 bytes")
		}
		s.signMethod = jwt.SigningMethodHS256
		s.signKey = secret
	case AlgEdDSA:
		if _, ok := priv.(ed25519.PrivateKey); !ok {
			return nil, errors.New("EdDSA requires an Ed25519 private key")
		}
		s.signMethod = jwt.SigningMethodEdDSA
		s.signKey = priv
	case AlgRS256:
		if _, ok := priv.(*rsa.PrivateKey); !ok {
			return nil, errors.New("RS256 requires an RSA private key")
		}
		s.signMethod = jwt.SigningMethodRS256
		s.signKey = priv
	default:
		return nil, fmt.Errorf("unknown JWT_SIGNING_ALG %q (want HS256, EdDSA, or RS256)", alg)
	}
	return s, nil
}

// Sign signs the claims with the active algorithm, attaching the kid header for
// asymmetric algorithms.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	tok := jwt.NewWithClaims(s.signMethod, claims)
	if s.kid != "" && s.activeAlg != AlgHS256 {
		tok.Header["kid"] = s.kid
	}
	return tok.SignedString(s.signKey)
}

// Verify parses and validates a token. The keyfunc selects the verification key
// strictly by the token's method: HMAC tokens are verified ONLY with the legacy
// secret (never the public key), asymmetric tokens ONLY with the public key.
func (s *Signer) Verify(tokenString string, opts ...jwt.ParserOption) (*jwt.Token, error) {
	return jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			if len(s.legacySecret) == 0 {
				return nil, errors.New("HS256 tokens are no longer accepted")
			}
			return s.legacySecret, nil
		case *jwt.SigningMethodEd25519:
			if pub, ok := s.publicKey.(ed25519.PublicKey); ok {
				return pub, nil
			}
			return nil, errors.New("no Ed25519 public key configured")
		case *jwt.SigningMethodRSA:
			if pub, ok := s.publicKey.(*rsa.PublicKey); ok {
				return pub, nil
			}
			return nil, errors.New("no RSA public key configured")
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
	}, opts...)
}

// JWKS returns the public JWK Set. It is a slice so a future rotation can
// publish the current and next keys together; today it holds zero or one key.
func (s *Signer) JWKS() JWKS {
	if s.jwk == nil {
		return JWKS{Keys: []jwk{}}
	}
	return JWKS{Keys: []jwk{*s.jwk}}
}

// JWKS is the JSON Web Key Set document.
type JWKS struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	// OKP (Ed25519)
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	// RSA
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
}

func parsePrivateKey(pemStr string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	// Try PKCS#8 first (covers Ed25519 and RSA), then PKCS#1 (RSA-only).
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
		return nil, fmt.Errorf("unsupported private key type %T", key)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported or malformed private key (want PKCS#8 or PKCS#1 PEM)")
}

// publicJWK builds the JWK and its RFC 7638 thumbprint kid for a public key.
func publicJWK(pub crypto.PublicKey) (string, *jwk, error) {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		x := base64.RawURLEncoding.EncodeToString(k)
		// Thumbprint over the required members in lexicographic order.
		kid := thumbprint(fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":%q}`, x))
		return kid, &jwk{Kty: "OKP", Use: "sig", Alg: AlgEdDSA, Kid: kid, Crv: "Ed25519", X: x}, nil
	case *rsa.PublicKey:
		n := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())
		kid := thumbprint(fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`, e, n))
		return kid, &jwk{Kty: "RSA", Use: "sig", Alg: AlgRS256, Kid: kid, N: n, E: e}, nil
	default:
		return "", nil, fmt.Errorf("unsupported public key type %T", pub)
	}
}

func thumbprint(canonicalJSON string) string {
	sum := sha256.Sum256([]byte(canonicalJSON))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// MarshalJWKS is a small convenience for handlers.
func (s *Signer) MarshalJWKS() ([]byte, error) {
	return json.Marshal(s.JWKS())
}
