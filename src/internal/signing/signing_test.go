package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func edKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	return pkcs8PEM(t, priv)
}

func rsaKeyPEM(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	return pkcs8PEM(t, priv)
}

func pkcs8PEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Minute).Unix()}
}

func TestRoundTripEdDSA(t *testing.T) {
	s, err := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tok, err := s.Sign(validClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parsed, err := s.Verify(tok, jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		t.Fatalf("Verify: %v (valid=%v)", err, parsed != nil && parsed.Valid)
	}
	if kid, _ := tok2kid(t, tok); kid == "" {
		t.Fatal("EdDSA token is missing a kid header")
	}
}

func TestRoundTripRS256(t *testing.T) {
	s, err := NewSigner(AlgRS256, rsaKeyPEM(t), nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tok, err := s.Sign(validClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.Verify(tok, jwt.WithExpirationRequired()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestTokenFromDifferentKeyRejected(t *testing.T) {
	signerA, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	signerB, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil) // different key

	tok, err := signerA.Sign(validClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signerB.Verify(tok); err == nil {
		t.Fatal("token signed by a different key must be rejected")
	}
}

func TestAlgConfusionHS256NotAcceptedAgainstPublicKey(t *testing.T) {
	pemStr := edKeyPEM(t)
	// Asymmetric-only signer: no legacy secret, so HS256 must be refused outright.
	s, err := NewSigner(AlgEdDSA, pemStr, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	// Classic confusion attempt: forge an HS256 token using the PUBLIC key bytes
	// as the HMAC secret. It must NOT verify.
	pub := s.publicKey.(ed25519.PublicKey)
	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	forgedStr, err := forged.SignedString([]byte(pub))
	if err != nil {
		t.Fatalf("sign forged: %v", err)
	}
	if _, err := s.Verify(forgedStr); err == nil {
		t.Fatal("HS256 token must never verify against the public key (alg confusion)")
	}
}

func TestTransitionAcceptsBothHS256AndAsymmetric(t *testing.T) {
	secret := []byte("transition-secret-at-least-32-bytes-long!!")
	// EdDSA active, but the legacy secret is still present.
	s, err := NewSigner(AlgEdDSA, edKeyPEM(t), secret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	// Asymmetric token from the signer verifies.
	asym, _ := s.Sign(validClaims())
	if _, err := s.Verify(asym, jwt.WithExpirationRequired()); err != nil {
		t.Fatalf("asymmetric token rejected during transition: %v", err)
	}

	// A legacy HS256 token signed with the real secret also verifies.
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	legacyStr, _ := legacy.SignedString(secret)
	if _, err := s.Verify(legacyStr, jwt.WithExpirationRequired()); err != nil {
		t.Fatalf("legacy HS256 token rejected during transition: %v", err)
	}
}

func TestHS256RejectedOnceSecretRemoved(t *testing.T) {
	secret := []byte("transition-secret-at-least-32-bytes-long!!")
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	legacyStr, _ := legacy.SignedString(secret)

	// Asymmetric-only signer (secret removed) must reject all HS256 tokens.
	s, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	if _, err := s.Verify(legacyStr); err == nil {
		t.Fatal("HS256 token must be rejected once the secret is removed")
	}
}

func TestExpiredRejected(t *testing.T) {
	s, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	expired := jwt.MapClaims{"sub": "u", "exp": time.Now().Add(-time.Minute).Unix()}
	tok, _ := s.Sign(expired)
	if _, err := s.Verify(tok, jwt.WithExpirationRequired()); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestExpirationRequired(t *testing.T) {
	s, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	noExp := jwt.MapClaims{"sub": "u"}
	tok, _ := s.Sign(noExp)
	if _, err := s.Verify(tok, jwt.WithExpirationRequired()); err == nil {
		t.Fatal("token without exp must be rejected when expiration is required")
	}
}

func TestJWKSReimportVerifiesEdDSA(t *testing.T) {
	s, _ := NewSigner(AlgEdDSA, edKeyPEM(t), nil)
	tok, _ := s.Sign(validClaims())

	set := s.JWKS()
	if len(set.Keys) != 1 {
		t.Fatalf("expected 1 JWK, got %d", len(set.Keys))
	}
	k := set.Keys[0]
	if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Use != "sig" || k.Alg != AlgEdDSA || k.Kid == "" {
		t.Fatalf("unexpected JWK shape: %+v", k)
	}

	// Reconstruct the public key from the JWK and verify the freshly signed token.
	rawX, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	pub := ed25519.PublicKey(rawX)
	parsed, err := jwt.Parse(tok, func(*jwt.Token) (any, error) { return pub, nil }, jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		t.Fatalf("reimported JWK failed to verify token: %v", err)
	}
}

func TestJWKSReimportVerifiesRS256(t *testing.T) {
	s, _ := NewSigner(AlgRS256, rsaKeyPEM(t), nil)
	tok, _ := s.Sign(validClaims())

	k := s.JWKS().Keys[0]
	if k.Kty != "RSA" || k.N == "" || k.E == "" {
		t.Fatalf("unexpected RSA JWK: %+v", k)
	}
	nBytes, _ := base64.RawURLEncoding.DecodeString(k.N)
	eBytes, _ := base64.RawURLEncoding.DecodeString(k.E)
	pub := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}
	parsed, err := jwt.Parse(tok, func(*jwt.Token) (any, error) { return pub, nil }, jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		t.Fatalf("reimported RSA JWK failed to verify token: %v", err)
	}
}

func TestHS256NoKidHeader(t *testing.T) {
	s, _ := NewSigner(AlgHS256, "", []byte("hs256-secret-at-least-32-bytes-long-xx"))
	tok, _ := s.Sign(validClaims())
	kid, ok := tok2kid(t, tok)
	if ok && kid != "" {
		t.Fatalf("HS256 token should carry no kid, got %q", kid)
	}
	if len(s.JWKS().Keys) != 0 {
		t.Fatal("HS256-only signer should publish no JWKS keys")
	}
}

// tok2kid parses the token header (without verifying) and returns the kid.
func tok2kid(t *testing.T, tokenString string) (string, bool) {
	t.Helper()
	parser := jwt.NewParser()
	tok, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse unverified: %v", err)
	}
	kid, ok := tok.Header["kid"].(string)
	return kid, ok
}
