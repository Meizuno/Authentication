# Rollout: HS256 → Asymmetric (EdDSA) Access-Token Signing

This is a **cross-service** migration. The auth service moves from signing
access tokens with a shared HMAC secret (`HS256`) — which every consumer holds
and could use to mint tokens — to **asymmetric** signing (`EdDSA`/Ed25519):
the auth service signs with a **private** key, and consumers verify with a
**public** key they cannot sign with.

Only the **access JWT** changes. **Refresh tokens are opaque random strings and
are unchanged**, so sessions survive the cutover and no user is forced to
re-login.

Consuming services (cannot be edited from this repo): **Notes**, **AI chat**.

> **Golden rule:** consumers must be able to verify the *new* tokens **before**
> the auth service starts issuing them. Consumers go first.

---

## Key facts the plan relies on

- Access-token TTL is **15 minutes**. After a cutover, any token signed the old
  way disappears within 15 minutes as it expires and clients call `/refresh`.
- `/refresh` returns a fresh access token signed with whatever algorithm is
  **currently active** — so the next refresh after a flip yields an asymmetric
  token automatically.
- The auth service **verifies both** during transition: asymmetric tokens
  always (when a key is configured), and legacy `HS256` tokens **only while
  `JWT_SECRET` is set**. Removing `JWT_SECRET` ends HS256 acceptance.
- Verification selects the key **strictly by token algorithm** (HMAC→secret,
  asymmetric→public key), so publishing the public key never enables an
  algorithm-confusion forgery.

---

## Phase 0 — Prepare (no behavior change)

Generate an Ed25519 keypair (PKCS#8 PEM):

```sh
openssl genpkey -algorithm ed25519 -out jwt_private.pem
openssl pkey -in jwt_private.pem -pubout -out jwt_public.pem   # for reference; consumers fetch via JWKS
```

On the **auth service**, set:

- `JWT_PRIVATE_KEY` (or `JWT_PRIVATE_KEY_FILE`) = the private key PEM.
- `JWT_SIGNING_ALG=HS256` — **still HS256**; we are not flipping yet.
- `JWT_ISSUER` = e.g. `https://auth.example.com`.
- `JWT_AUDIENCE` = the consumer set, e.g. `notes,ai-chat`.
- Keep `JWT_SECRET` as-is.

Deploy. Effects:

- Signing is unchanged (still HS256), but tokens now also carry `iss`/`aud`.
- **`GET /.well-known/jwks.json` now serves the public key** (with its `kid`),
  even though signing is still HS256. The key is reachable for consumers to
  fetch and cache.

Verify: `curl https://auth.example.com/.well-known/jwks.json` returns a JWK Set
containing one `OKP`/`Ed25519` key with a `kid`, `use: "sig"`, `alg: "EdDSA"`.

## Phase 1 — Consumers accept BOTH (consumers first)

Update **Notes** and **AI chat** to verify access tokens by **either**:

1. the existing shared `HS256` secret (unchanged), **and**
2. the auth service's **JWKS**: fetch `/.well-known/jwks.json`, **cache** it,
   and select the key by the token's `kid` header; verify `EdDSA` signatures
   with it.

Each consumer must, for every request:

- Branch on the token's `alg`/method: `HS256` → verify with the shared secret;
  `EdDSA` → verify with the JWKS public key for the token's `kid`. **Never**
  feed the public key into an HMAC verifier.
- Enforce expiry, and validate `iss` == the auth issuer and that `aud` contains
  the consumer's own audience.
- Prefer verifying **locally via JWKS** rather than calling the auth service's
  `/validate` per request (no per-request round-trip; cache the JWKS, refresh
  on unknown `kid` or periodically).

Deploy consumers. Nothing breaks: the auth service still signs `HS256`, which
consumers still accept. Consumers are now **ready** for asymmetric tokens.

> Consumer JWKS caching: cache by `kid`; on encountering an unknown `kid`,
> refetch the JWKS once (this is what makes future key rotation seamless).

## Phase 2 — Flip the auth service to EdDSA

On the auth service, set `JWT_SIGNING_ALG=EdDSA` (keep `JWT_PRIVATE_KEY`,
`JWT_SECRET`, `iss`/`aud`). Deploy.

Effects:

- New access tokens are **EdDSA**, carrying `kid` + `iss` + `aud`. Consumers
  verify them via the JWKS key cached in Phase 1.
- In-flight **HS256 access tokens (≤ 15 min old)** still verify on consumers
  (they kept the secret) and on the auth service (`JWT_SECRET` still set).
- **Refresh tokens are opaque and unchanged**, so existing sessions continue;
  the next `/refresh` yields EdDSA tokens. **No forced re-login.**

## Phase 3 — Wait out the old tokens

Wait **> access-token TTL**. Use **30 minutes** for margin (TTL is 15 min).
After this window, no valid `HS256` access tokens remain anywhere — every
active access token is EdDSA.

## Phase 4 — Consumers drop the HS256 secret

Update **Notes** and **AI chat** to remove the shared `HS256` secret and verify
**asymmetric-only** (JWKS). Deploy. Consumers can no longer mint tokens — they
hold only the public key.

## Phase 5 — Auth service drops the shared secret

On the auth service, **remove `JWT_SECRET`**. Deploy. Now:

- The auth service rejects all `HS256` tokens (no secret to verify them).
- **No shared signing secret exists anywhere.** Only the auth service holds the
  private key; everyone else verifies with the public key.

Verify: a token forged with `HS256` (using any secret, including the public key
bytes) is rejected by both the auth service and consumers.

---

## Rollback

The cutover is reversible **without forced re-login** up to (not including)
Phase 4:

- **Anytime before Phase 4** (consumers still accept HS256): set
  `JWT_SIGNING_ALG=HS256` on the auth service and redeploy. New tokens are
  HS256 again, which consumers still accept. This is a **safe, instant revert**
  — state this is the primary rollback lever.
- After **Phase 4** (consumers are asymmetric-only): to roll back you must first
  re-deploy consumers to accept HS256 again **before** flipping the auth service
  back, otherwise consumers will reject the HS256 tokens. So do not start Phase 4
  until you are confident in the asymmetric path.
- **Phase 5 is the point of no return** for the shared secret: once `JWT_SECRET`
  is gone, returning to HS256 requires distributing a new secret to all parties
  (effectively a fresh migration).

## Key rotation (future)

The JWKS is a **set**: publish the next key alongside the current one (the
endpoint and consumers already handle multiple keys keyed by `kid`). Sign with
the new key once consumers have cached it; retire the old key after the TTL
window. No consumer code change is required beyond what Phase 1 already
established.

## Quick reference — auth service env per phase

| Phase | JWT_SIGNING_ALG | JWT_PRIVATE_KEY | JWT_SECRET | JWT_ISSUER / JWT_AUDIENCE |
|------:|-----------------|-----------------|------------|---------------------------|
| 0     | HS256           | set             | set        | set                       |
| 1     | HS256           | set             | set        | set                       |
| 2     | **EdDSA**       | set             | set        | set                       |
| 3     | EdDSA           | set             | set        | set                       |
| 4     | EdDSA           | set             | set        | set                       |
| 5     | EdDSA           | set             | **unset**  | set                       |
