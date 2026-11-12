package embyserver

import (
	"database/sql"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Authenticator interface {
	Authenticate(token string) (User, error)
	Login(username, password, device string) (LoginResult, error)
	Revoke(token string) error
}

type LoginResult struct {
	User           User
	AccessToken    string
	DeviceID       string
	LastLoginError error
}

type LoginFailureKind int

const (
	LoginFailureCredentials LoginFailureKind = iota + 1
	LoginFailureTokenWrite
)

type LoginFailure struct {
	Kind   LoginFailureKind
	Reason string
	Err    error
}

func (e *LoginFailure) Error() string {
	if e == nil {
		return "login failed"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Reason
}

func (e *LoginFailure) Unwrap() error {
	return e.Err
}

type authRepository interface {
	userForToken(tokenHash string, now int64) (User, error)
	apiKeyExists(keyHash string) (bool, error)
	userByName(username string) (User, error)
	createToken(tokenHash, userID, device string, expires int64) error
	revokeToken(tokenHash string) error
	recordLogin(userID string, lastLogin int64) error
}

type authenticationService struct {
	repository authRepository
}

func (a authenticationService) Revoke(token string) error {
	if token == "" {
		return errors.New("missing token")
	}
	return a.repository.revokeToken(digest(token))
}

func (a authenticationService) Authenticate(token string) (User, error) {
	var user User
	if token == "" {
		return user, errors.New("missing token")
	}

	hash := digest(token)
	user, err := a.repository.userForToken(hash, time.Now().Unix())
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return user, err
	}

	exists, err := a.repository.apiKeyExists(hash)
	if err != nil {
		return user, err
	}
	if exists {
		user.API = true
		return user, nil
	}
	return user, sql.ErrNoRows
}

func (a authenticationService) Login(username, password, device string) (LoginResult, error) {
	user, err := a.repository.userByName(username)
	if err != nil {
		reason := "用户查询失败"
		if errors.Is(err, sql.ErrNoRows) {
			reason = "用户不存在"
		}
		return LoginResult{}, &LoginFailure{Kind: LoginFailureCredentials, Reason: reason, Err: err}
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Hash), []byte(password)); err != nil {
		return LoginResult{}, &LoginFailure{Kind: LoginFailureCredentials, Reason: "密码错误", Err: err}
	}

	if device == "" {
		device = id()
	}
	token := id() + id()
	if err := a.repository.createToken(digest(token), user.ID, device, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
		return LoginResult{}, &LoginFailure{Kind: LoginFailureTokenWrite, Err: err}
	}

	return LoginResult{
		User:           user,
		AccessToken:    token,
		DeviceID:       device,
		LastLoginError: a.repository.recordLogin(user.ID, time.Now().Unix()),
	}, nil
}

type sqlAuthRepository struct {
	db *Database
}

func (r sqlAuthRepository) userForToken(tokenHash string, now int64) (User, error) {
	var user User
	err := r.db.QueryRow(`SELECT u.id,u.name,u.hash,u.admin,u.first_admin,u.max_devices,t.device
		FROM users u JOIN tokens t ON t.user_id=u.id WHERE t.hash=? AND t.expires>?`, tokenHash, now).
		Scan(&user.ID, &user.Name, &user.Hash, &user.Admin, &user.First, &user.Max, &user.Device)
	return user, err
}

func (r sqlAuthRepository) apiKeyExists(keyHash string) (bool, error) {
	var keyID string
	err := r.db.QueryRow("SELECT id FROM api_keys WHERE hash=?", keyHash).Scan(&keyID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (r sqlAuthRepository) userByName(username string) (User, error) {
	var user User
	err := r.db.QueryRow("SELECT id,name,hash,admin,first_admin,max_devices FROM users WHERE lower(name)=lower(?)", username).
		Scan(&user.ID, &user.Name, &user.Hash, &user.Admin, &user.First, &user.Max)
	return user, err
}

func (r sqlAuthRepository) createToken(tokenHash, userID, device string, expires int64) error {
	_, err := r.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", tokenHash, userID, device, expires)
	return err
}

func (r sqlAuthRepository) revokeToken(tokenHash string) error {
	_, err := r.db.Exec("DELETE FROM tokens WHERE hash=?", tokenHash)
	return err
}

func (r sqlAuthRepository) recordLogin(userID string, lastLogin int64) error {
	_, err := r.db.Exec("INSERT INTO user_logins VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET last_login=excluded.last_login", userID, lastLogin)
	return err
}
