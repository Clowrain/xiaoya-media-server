package embyserver

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeAuthRepository struct {
	user          User
	userErr       error
	loginUser     User
	loginErr      error
	keyExists     bool
	keyErr        error
	tokenHash     string
	keyHash       string
	createdHash   string
	createdUser   string
	createdDevice string
	createdExpiry int64
	lastLoginUser string
	lastLoginAt   int64
	tokenWriteErr error
	lastLoginErr  error
	tokenCalls    int
	keyCalls      int
}

func (r *fakeAuthRepository) userForToken(tokenHash string, _ int64) (User, error) {
	r.tokenHash = tokenHash
	r.tokenCalls++
	return r.user, r.userErr
}

func (r *fakeAuthRepository) apiKeyExists(keyHash string) (bool, error) {
	r.keyHash = keyHash
	r.keyCalls++
	return r.keyExists, r.keyErr
}

func (r *fakeAuthRepository) userByName(string) (User, error) {
	return r.loginUser, r.loginErr
}

func (r *fakeAuthRepository) createToken(tokenHash, userID, device string, expires int64) error {
	r.createdHash = tokenHash
	r.createdUser = userID
	r.createdDevice = device
	r.createdExpiry = expires
	return r.tokenWriteErr
}

func (r *fakeAuthRepository) revokeToken(string) error { return nil }

func (r *fakeAuthRepository) recordLogin(userID string, lastLogin int64) error {
	r.lastLoginUser = userID
	r.lastLoginAt = lastLogin
	return r.lastLoginErr
}

func TestTokenAuthenticatorUserToken(t *testing.T) {
	repository := &fakeAuthRepository{user: User{ID: "user-1", Name: "viewer"}}
	authenticator := authenticationService{repository: repository}

	user, err := authenticator.Authenticate("user-token")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "user-1" || user.API {
		t.Fatalf("unexpected authenticated user: %+v", user)
	}
	if repository.tokenHash != digest("user-token") || repository.tokenCalls != 1 || repository.keyCalls != 0 {
		t.Fatalf("unexpected repository calls: %+v", repository)
	}
}

func TestTokenAuthenticatorDoesNotMaskTokenStoreErrorsWithAPIKeyFallback(t *testing.T) {
	wantErr := errors.New("token storage unavailable")
	repository := &fakeAuthRepository{userErr: wantErr, keyExists: true}
	_, err := (authenticationService{repository: repository}).Authenticate("key")
	if !errors.Is(err, wantErr) {
		t.Fatalf("authentication error = %v, want token lookup error %v", err, wantErr)
	}
	if repository.keyCalls != 0 {
		t.Fatalf("API key lookup must not run after token storage failure; calls=%d", repository.keyCalls)
	}
}

func TestPruneLoginAttemptsRemovesExpiredClients(t *testing.T) {
	now := time.Now()
	attempts := map[string][]time.Time{
		"active":  {now.Add(-time.Second), now},
		"expired": {now.Add(-2 * time.Minute)},
	}
	pruneLoginAttempts(attempts, now.Add(-time.Minute))
	if len(attempts) != 1 || len(attempts["active"]) != 2 {
		t.Fatalf("unexpected pruned login attempts: %+v", attempts)
	}
}

func TestLoginClientIPSupportsIPv4AndIPv6(t *testing.T) {
	for addr, want := range map[string]string{
		"192.0.2.10:4567":    "192.0.2.10",
		"[2001:db8::1]:4567": "2001:db8::1",
		"2001:db8::1":        "2001:db8::1",
	} {
		if got := loginClientIP(addr); got != want {
			t.Errorf("loginClientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestLoginFailureErrorWithoutUnderlyingError(t *testing.T) {
	failure := &LoginFailure{Reason: "credential failure"}
	if got := failure.Error(); got != "credential failure" {
		t.Fatalf("Error() = %q, want reason", got)
	}
}

func TestTokenAuthenticatorAPIKeyFallback(t *testing.T) {
	repository := &fakeAuthRepository{userErr: sql.ErrNoRows, keyExists: true}
	authenticator := authenticationService{repository: repository}

	user, err := authenticator.Authenticate("admin-key")
	if err != nil {
		t.Fatal(err)
	}
	if !user.API {
		t.Fatalf("API key must be marked as API user: %+v", user)
	}
	if repository.keyHash != digest("admin-key") || repository.tokenCalls != 1 || repository.keyCalls != 1 {
		t.Fatalf("unexpected repository calls: %+v", repository)
	}
}

func TestTokenAuthenticatorMissingAndInvalidToken(t *testing.T) {
	repository := &fakeAuthRepository{userErr: sql.ErrNoRows}
	authenticator := authenticationService{repository: repository}

	if _, err := authenticator.Authenticate(""); err == nil || err.Error() != "missing token" {
		t.Fatalf("missing token error = %v", err)
	}
	if repository.tokenCalls != 0 || repository.keyCalls != 0 {
		t.Fatalf("empty token should not query the repository: %+v", repository)
	}

	if _, err := authenticator.Authenticate("invalid"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid token error = %v, want sql.ErrNoRows", err)
	}
}

func TestTokenAuthenticatorPropagatesAPIKeyLookupError(t *testing.T) {
	wantErr := errors.New("api key storage unavailable")
	repository := &fakeAuthRepository{userErr: sql.ErrNoRows, keyErr: wantErr}
	authenticator := authenticationService{repository: repository}

	if _, err := authenticator.Authenticate("token"); !errors.Is(err, wantErr) {
		t.Fatalf("authentication error = %v, want %v", err, wantErr)
	}
}

func TestAuthenticationServiceRevokeToken(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO tokens(hash,user_id,device,expires) VALUES(?,?,?,?)", digest("plain-token"), "test-user", "device-1", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	service := authenticationService{repository: sqlAuthRepository{db: app.db}}
	if err := service.Revoke("plain-token"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := app.db.QueryRow("SELECT count(*) FROM tokens WHERE hash=?", digest("plain-token")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked token count = %d, err=%v", count, err)
	}
}

func TestAuthenticationServiceLogin(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeAuthRepository{loginUser: User{ID: "user-1", Name: "viewer", Hash: string(passwordHash)}}
	service := authenticationService{repository: repository}
	started := time.Now().Unix()

	result, err := service.Login("viewer", "correct-password", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.User.ID != "user-1" || result.AccessToken == "" || result.DeviceID != "device-1" {
		t.Fatalf("unexpected login result: %+v", result)
	}
	if repository.createdHash != digest(result.AccessToken) || repository.createdUser != "user-1" || repository.createdDevice != "device-1" {
		t.Fatalf("token was not persisted as a hash for the authenticated device: %+v", repository)
	}
	if repository.createdExpiry < started+29*24*60*60 || repository.createdExpiry > started+30*24*60*60+1 {
		t.Fatalf("unexpected token expiry: %d", repository.createdExpiry)
	}
	if repository.lastLoginUser != "user-1" || repository.lastLoginAt < started {
		t.Fatalf("last-login record not written: %+v", repository)
	}
}

func TestAuthenticationServiceLoginFailures(t *testing.T) {
	t.Run("unknown user", func(t *testing.T) {
		repository := &fakeAuthRepository{loginErr: sql.ErrNoRows}
		service := authenticationService{repository: repository}
		_, err := service.Login("missing", "password", "device")
		var failure *LoginFailure
		if !errors.As(err, &failure) || failure.Kind != LoginFailureCredentials || failure.Reason != "用户不存在" {
			t.Fatalf("unexpected login failure: %v", err)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		repository := &fakeAuthRepository{loginUser: User{Hash: string(passwordHash)}}
		service := authenticationService{repository: repository}
		_, err = service.Login("viewer", "wrong-password", "device")
		var failure *LoginFailure
		if !errors.As(err, &failure) || failure.Kind != LoginFailureCredentials || failure.Reason != "密码错误" {
			t.Fatalf("unexpected login failure: %v", err)
		}
	})

	t.Run("token persistence failure", func(t *testing.T) {
		passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		writeErr := errors.New("token storage unavailable")
		repository := &fakeAuthRepository{loginUser: User{ID: "user-1", Hash: string(passwordHash)}, tokenWriteErr: writeErr}
		service := authenticationService{repository: repository}
		_, err = service.Login("viewer", "correct-password", "device")
		var failure *LoginFailure
		if !errors.As(err, &failure) || failure.Kind != LoginFailureTokenWrite || !errors.Is(err, writeErr) {
			t.Fatalf("unexpected login failure: %v", err)
		}
		if repository.lastLoginAt != 0 {
			t.Fatal("last-login record must not be written when token persistence fails")
		}
	})

	t.Run("last-login failure remains non-fatal", func(t *testing.T) {
		passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		loginErr := errors.New("login history unavailable")
		repository := &fakeAuthRepository{loginUser: User{ID: "user-1", Hash: string(passwordHash)}, lastLoginErr: loginErr}
		service := authenticationService{repository: repository}
		result, err := service.Login("viewer", "correct-password", "device")
		if err != nil {
			t.Fatalf("login history failure must not reject the login: %v", err)
		}
		if !errors.Is(result.LastLoginError, loginErr) {
			t.Fatalf("last-login error was not surfaced to caller: %v", result.LastLoginError)
		}
	})
}
