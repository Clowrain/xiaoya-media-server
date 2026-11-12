package embyserver

import (
	"errors"
	"log"
	"net"
	"net/http"
	"time"
)

func pruneLoginAttempts(attempts map[string][]time.Time, cutoff time.Time) {
	for key, timestamps := range attempts {
		active := timestamps[:0]
		for _, timestamp := range timestamps {
			if timestamp.After(cutoff) {
				active = append(active, timestamp)
			}
		}
		if len(active) == 0 {
			delete(attempts, key)
		} else {
			attempts[key] = active
		}
	}
}

func loginClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		return ip.String()
	}
	return remoteAddr
}

func (a *App) auth(r *http.Request) (User, error) {
	return a.resolveAuthenticator().Authenticate(token(r))
}

func (a *App) resolveAuthenticator() Authenticator {
	if a.authenticator != nil {
		return a.authenticator
	}
	return authenticationService{repository: sqlAuthRepository{db: a.db}}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	a.loginMu.Lock()
	if a.attempts == nil {
		a.attempts = make(map[string][]time.Time)
	}
	now := time.Now()
	pruneLoginAttempts(a.attempts, now.Add(-time.Minute))
	ip := loginClientIP(r.RemoteAddr)
	recent := a.attempts[ip]
	if len(recent) >= 15 {
		a.loginMu.Unlock()
		a.authWarning(r, "", "登录过于频繁")
		fail(w, http.StatusTooManyRequests, "登录过于频繁，请稍后再试")
		return
	}
	a.attempts[ip] = append(recent, now)
	a.loginMu.Unlock()

	var credentials loginCredentials
	if !loginBody(w, r, &credentials) {
		return
	}
	result, err := a.resolveAuthenticator().Login(credentials.Username, credentials.Pw, device(r))
	if err != nil {
		var failure *LoginFailure
		if !errors.As(err, &failure) {
			fail(w, http.StatusInternalServerError, "登录失败")
			return
		}
		if failure.Kind == LoginFailureTokenWrite {
			fail(w, http.StatusInternalServerError, "登录写入失败")
			return
		}
		a.authWarning(r, credentials.Username, failure.Reason)
		fail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if result.LastLoginError != nil {
		log.Printf("update last login timestamp failed: %v", result.LastLoginError)
	}
	a.loginSuccess(r, result.User, result.DeviceID)
	respond(w, M{"User": a.userDTO(result.User), "AccessToken": result.AccessToken, "ServerId": a.serverID, "SessionInfo": a.loginSession(r, result.User, result.DeviceID)})
}
