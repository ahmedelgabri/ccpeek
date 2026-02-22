# Authentication System Overhaul

## Current State

- Session-based auth with Redis store
- No multi-factor authentication
- Password hashing uses bcrypt (cost 10)

## Target State

- JWT-based stateless authentication
- TOTP-based MFA support
- OAuth2 social login (Google, GitHub)
- Refresh token rotation with family detection

## Implementation Plan

### Token Architecture

```
Access Token (15 min TTL)
├── sub: user_id
├── exp: expiry timestamp
├── roles: ["user", "admin"]
└── jti: unique token ID

Refresh Token (30 day TTL)
├── sub: user_id
├── family: token family UUID
├── generation: rotation counter
└── jti: unique token ID
```

### MFA Flow

1. User logs in with email/password
2. Server returns `mfa_required: true` with temp token
3. Client prompts for TOTP code
4. Server validates TOTP, issues full access + refresh tokens

### OAuth2 Integration

- Use Authorization Code flow with PKCE
- Map external provider IDs to internal user accounts
- Allow account linking (multiple providers per user)

### Database Changes

New tables:

- `refresh_tokens` (id, user_id, family, generation, expires_at)
- `mfa_secrets` (user_id, secret, verified_at)
- `oauth_connections` (user_id, provider, provider_id, access_token)

### Endpoints

| Method | Path                           | Description          |
| ------ | ------------------------------ | -------------------- |
| POST   | /auth/login                    | Email/password login |
| POST   | /auth/mfa/verify               | Verify TOTP code     |
| POST   | /auth/refresh                  | Rotate refresh token |
| POST   | /auth/logout                   | Revoke token family  |
| GET    | /auth/oauth/:provider          | OAuth2 redirect      |
| GET    | /auth/oauth/:provider/callback | OAuth2 callback      |
