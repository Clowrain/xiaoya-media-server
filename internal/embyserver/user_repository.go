package embyserver

import (
	"database/sql"
	"errors"
	"strings"
)

type UserAccount struct {
	ID, Name, Hash string
	Admin, First   bool
	MaxDevices     int
	LastLogin      int64
}

type UserIdentityFailure uint8

const (
	UserIdentityPersistenceFailure UserIdentityFailure = iota + 1
	UserIdentityNameFailure
	UserIdentityPasswordFailure
)

type UserIdentityUpdateError struct {
	Failure UserIdentityFailure
	Err     error
}

func (e *UserIdentityUpdateError) Error() string { return e.Err.Error() }
func (e *UserIdentityUpdateError) Unwrap() error { return e.Err }

type UserPlaybackState struct {
	Position int64
	Played   bool
}

type UserItemPredicate struct {
	SQL  string
	Args []any
}

type UserFavoriteTarget uint8

const (
	FavoriteMediaItem UserFavoriteTarget = iota
	FavoritePerson
)

type UserRepository interface {
	GetAccount(userID string) (UserAccount, error)
	ListAccounts() ([]UserAccount, error)
	IsFirstAdmin(userID string) (bool, error)
	SetAccountSettings(userID string, maxDevices int, admin bool) error
	CreateAccount(account UserAccount, allowPlayback *bool) error
	CountAccounts() (int, error)
	SetPlaybackAllowed(userID string, allowed bool) error
	CanPlay(userID string) (bool, error)
	UpdatePasswordAndRevokeTokens(userID, passwordHash string) error
	UpdateIdentity(userID, name, passwordHash string) (bool, error)
	DeleteUser(userID string) error
	GetPlaybackState(userID, itemID string) (UserPlaybackState, error)
	ListPlaybackStates(userID string, itemIDs []string) (map[string]UserPlaybackState, error)
	ListFavorites(userID string, itemIDs []string) (map[string]bool, error)
	ResumableItemPredicate(userID string) UserItemPredicate
	ResumeSeed(userID, libraryID string) (Item, error)
	PlayedItemPredicate(userID string, excludePlayed bool) UserItemPredicate
	FavoritePredicate(userID string, target UserFavoriteTarget, excludeFavorite bool) UserItemPredicate
	NextUpPredicate(userID string) UserItemPredicate
	IsFavorite(userID, itemID string) (bool, error)
	SetFavorite(userID, itemID string, favorite bool, createdAt int64) error
	SetPlayedItem(userID, itemID string, played bool) error
	SetPlayedTree(userID, itemID string, played bool) error
	SetPlaybackPosition(userID, itemID string, ticks int64) error
}

type sqlUserRepository struct {
	db *Database
}

func (r sqlUserRepository) GetAccount(userID string) (UserAccount, error) {
	if r.db == nil {
		return UserAccount{}, errors.New("user database is unavailable")
	}
	var account UserAccount
	err := r.db.QueryRow("SELECT id,name,hash,admin,first_admin,max_devices FROM users WHERE id=?", userID).
		Scan(&account.ID, &account.Name, &account.Hash, &account.Admin, &account.First, &account.MaxDevices)
	return account, err
}

func (r sqlUserRepository) ListAccounts() ([]UserAccount, error) {
	if r.db == nil {
		return nil, errors.New("user database is unavailable")
	}
	rows, err := r.db.Query(`SELECT u.id,u.name,u.hash,u.admin,u.first_admin,u.max_devices,COALESCE(l.last_login,0)
		FROM users u LEFT JOIN user_logins l ON l.user_id=u.id ORDER BY u.first_admin DESC,u.name`)
	withLastLogin := true
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "user_logins") {
		// Preserve account listing for lightweight legacy schemas where login history is optional.
		withLastLogin = false
		rows, err = r.db.Query("SELECT id,name,hash,admin,first_admin,max_devices FROM users ORDER BY first_admin DESC,name")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]UserAccount, 0)
	for rows.Next() {
		var account UserAccount
		if withLastLogin {
			err = rows.Scan(&account.ID, &account.Name, &account.Hash, &account.Admin, &account.First, &account.MaxDevices, &account.LastLogin)
		} else {
			err = rows.Scan(&account.ID, &account.Name, &account.Hash, &account.Admin, &account.First, &account.MaxDevices)
		}
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r sqlUserRepository) IsFirstAdmin(userID string) (bool, error) {
	if r.db == nil {
		return false, errors.New("user database is unavailable")
	}
	var firstAdmin bool
	err := r.db.QueryRow("SELECT first_admin FROM users WHERE id=?", userID).Scan(&firstAdmin)
	return firstAdmin, err
}

func (r sqlUserRepository) SetAccountSettings(userID string, maxDevices int, admin bool) error {
	if r.db == nil {
		return errors.New("user database is unavailable")
	}
	_, err := r.db.Exec("UPDATE users SET max_devices=?,admin=? WHERE id=?", maxDevices, admin, userID)
	return err
}

func (r sqlUserRepository) CreateAccount(account UserAccount, allowPlayback *bool) error {
	if r.db == nil {
		return errors.New("user database is unavailable")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO users(id,name,hash,admin,first_admin,max_devices) VALUES(?,?,?,?,?,?)", account.ID, account.Name, account.Hash, account.Admin, account.First, account.MaxDevices); err != nil {
		return err
	}
	if allowPlayback != nil {
		if _, err = tx.Exec("INSERT INTO user_playback(user_id,allowed) VALUES(?,?)", account.ID, *allowPlayback); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r sqlUserRepository) CountAccounts() (int, error) {
	if r.db == nil {
		return 0, errors.New("user database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM users").Scan(&count)
	return count, err
}

func (r sqlUserRepository) SetPlaybackAllowed(userID string, allowed bool) error {
	if r.db == nil {
		return errors.New("user database is unavailable")
	}
	_, err := r.db.Exec("INSERT INTO user_playback(user_id,allowed) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET allowed=excluded.allowed", userID, allowed)
	return err
}

func (r sqlUserRepository) CanPlay(userID string) (bool, error) {
	if r.db == nil {
		return false, errors.New("user database is unavailable")
	}
	var allowed bool
	err := r.db.QueryRow("SELECT allowed FROM user_playback WHERE user_id=?", userID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return allowed, err
}

func (r sqlUserRepository) UpdatePasswordAndRevokeTokens(userID, passwordHash string) error {
	if r.db == nil {
		return errors.New("user database is unavailable")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET hash=? WHERE id=?", passwordHash, userID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM tokens WHERE user_id=?", userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r sqlUserRepository) UpdateIdentity(userID, name, passwordHash string) (bool, error) {
	if r.db == nil {
		return false, &UserIdentityUpdateError{Failure: UserIdentityPersistenceFailure, Err: errors.New("user database is unavailable")}
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, &UserIdentityUpdateError{Failure: UserIdentityPersistenceFailure, Err: err}
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE users SET name=? WHERE id=?", name, userID)
	if err != nil {
		return false, &UserIdentityUpdateError{Failure: UserIdentityNameFailure, Err: err}
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, &UserIdentityUpdateError{Failure: UserIdentityPersistenceFailure, Err: err}
	}
	if rowsAffected == 0 {
		return false, nil
	}
	if passwordHash != "" {
		if _, err := tx.Exec("UPDATE users SET hash=? WHERE id=?", passwordHash, userID); err != nil {
			return false, &UserIdentityUpdateError{Failure: UserIdentityPasswordFailure, Err: err}
		}
		if _, err := tx.Exec("DELETE FROM tokens WHERE user_id=?", userID); err != nil {
			return false, &UserIdentityUpdateError{Failure: UserIdentityPasswordFailure, Err: err}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, &UserIdentityUpdateError{Failure: UserIdentityPersistenceFailure, Err: err}
	}
	return true, nil
}

func (r sqlUserRepository) DeleteUser(userID string) error {
	if r.db == nil {
		return errors.New("user database is unavailable")
	}
	_, err := r.db.Exec("DELETE FROM users WHERE id=?", userID)
	return err
}

func (r sqlUserRepository) GetPlaybackState(userID, itemID string) (UserPlaybackState, error) {
	var state UserPlaybackState
	if r.db == nil || userID == "" || itemID == "" {
		return state, nil
	}
	err := r.db.QueryRow("SELECT position,played FROM userdata WHERE user_id=? AND item=?", userID, itemID).Scan(&state.Position, &state.Played)
	if errors.Is(err, sql.ErrNoRows) {
		return UserPlaybackState{}, nil
	}
	return state, err
}

func (r sqlUserRepository) ListPlaybackStates(userID string, itemIDs []string) (map[string]UserPlaybackState, error) {
	states := make(map[string]UserPlaybackState, len(itemIDs))
	if r.db == nil || userID == "" || len(itemIDs) == 0 {
		return states, nil
	}
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, userID)
	for _, itemID := range itemIDs {
		args = append(args, itemID)
	}
	query := "SELECT item,position,played FROM userdata WHERE user_id=? AND item IN (" + strings.TrimRight(strings.Repeat("?,", len(itemIDs)), ",") + ")"
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID string
		var state UserPlaybackState
		if err := rows.Scan(&itemID, &state.Position, &state.Played); err != nil {
			return nil, err
		}
		states[itemID] = state
	}
	return states, rows.Err()
}

func (r sqlUserRepository) ListFavorites(userID string, itemIDs []string) (map[string]bool, error) {
	favorites := make(map[string]bool, len(itemIDs))
	if r.db == nil || userID == "" || len(itemIDs) == 0 {
		return favorites, nil
	}
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, userID)
	for _, itemID := range itemIDs {
		args = append(args, itemID)
	}
	query := "SELECT item FROM user_favorites WHERE user_id=? AND item IN (" + strings.TrimRight(strings.Repeat("?,", len(itemIDs)), ",") + ")"
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID string
		if err := rows.Scan(&itemID); err != nil {
			return nil, err
		}
		favorites[itemID] = true
	}
	return favorites, rows.Err()
}

func (r sqlUserRepository) ResumableItemPredicate(userID string) UserItemPredicate {
	return UserItemPredicate{
		SQL:  "id IN (SELECT item FROM userdata WHERE user_id=? AND position>0 AND played=0)",
		Args: []any{userID},
	}
}

func (r sqlUserRepository) ResumeSeed(userID, libraryID string) (Item, error) {
	if r.db == nil {
		return Item{}, errors.New("user database is unavailable")
	}
	var item Item
	err := r.db.QueryRow(`SELECT i.id,i.name FROM items i
		JOIN userdata d ON d.item=i.id
		LEFT JOIN resume_activity ra ON ra.item=i.id AND ra.user_id=d.user_id
		WHERE d.user_id=? AND (d.played=1 OR d.position>0) AND i.kind='Movie'
		AND (?='' OR i.lib=?) ORDER BY ra.updated DESC,i.id LIMIT 1`, userID, libraryID, libraryID).
		Scan(&item.ID, &item.Name)
	return item, err
}

func (r sqlUserRepository) PlayedItemPredicate(userID string, excludePlayed bool) UserItemPredicate {
	operator := "IN"
	if excludePlayed {
		operator = "NOT IN"
	}
	return UserItemPredicate{
		SQL:  "id " + operator + " (SELECT item FROM userdata WHERE user_id=? AND played=1)",
		Args: []any{userID},
	}
}

func (r sqlUserRepository) FavoritePredicate(userID string, target UserFavoriteTarget, excludeFavorite bool) UserItemPredicate {
	column := "id"
	if target == FavoritePerson {
		column = "person"
	}
	operator := "IN"
	if excludeFavorite {
		operator = "NOT IN"
	}
	return UserItemPredicate{
		SQL:  column + " " + operator + " (SELECT item FROM user_favorites WHERE user_id=?)",
		Args: []any{userID},
	}
}

func (r sqlUserRepository) NextUpPredicate(userID string) UserItemPredicate {
	return UserItemPredicate{
		SQL: `id IN (
	 SELECT pick.id FROM (SELECT id FROM items WHERE kind='Series' AND id IN (
	   SELECT CASE WHEN p.kind='Season' THEN p.parent ELSE ep.parent END
	   FROM items ep LEFT JOIN items p ON p.id=ep.parent JOIN userdata ud ON ud.item=ep.id AND ud.user_id=?
	   WHERE (ud.played=1 OR ud.position>0)
	 )) s
	 CROSS JOIN LATERAL (
	 SELECT e.id FROM (SELECT s.id UNION ALL SELECT id FROM items WHERE parent=s.id AND kind='Season') parents
	 CROSS JOIN LATERAL (SELECT i.id,i.season,i.episode FROM items i WHERE i.parent=parents.id AND i.kind='Episode'
	 AND NOT EXISTS (SELECT 1 FROM userdata d WHERE d.user_id=? AND d.item=i.id AND d.played=1)
	 ORDER BY i.season,i.episode,i.id LIMIT 1) e
	 ORDER BY e.season,e.episode,e.id LIMIT 1) pick)`,
		Args: []any{userID, userID},
	}
}

func (r sqlUserRepository) IsFavorite(userID, itemID string) (bool, error) {
	if r.db == nil || userID == "" || itemID == "" {
		return false, nil
	}
	var exists int
	err := r.db.QueryRow("SELECT 1 FROM user_favorites WHERE user_id=? AND item=?", userID, itemID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists > 0, err
}

func (r sqlUserRepository) SetFavorite(userID, itemID string, favorite bool, createdAt int64) error {
	if r.db == nil {
		return nil
	}
	if favorite {
		_, err := r.db.Exec("INSERT INTO user_favorites(user_id,item,created_at) VALUES(?,?,?) ON CONFLICT(user_id,item) DO NOTHING", userID, itemID, createdAt)
		return err
	}
	_, err := r.db.Exec("DELETE FROM user_favorites WHERE user_id=? AND item=?", userID, itemID)
	return err
}

func (r sqlUserRepository) SetPlayedItem(userID, itemID string, played bool) error {
	if r.db == nil {
		return nil
	}
	playedValue := int64(0)
	if played {
		playedValue = 1
	}
	_, err := r.db.Exec("INSERT INTO userdata(user_id,item,played,position) VALUES(?,?,?,0) ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0", userID, itemID, playedValue)
	return err
}

func (r sqlUserRepository) SetPlayedTree(userID, itemID string, played bool) error {
	if r.db == nil {
		return nil
	}
	playedValue := int64(0)
	if played {
		playedValue = 1
	}
	var errs []error
	if err := r.SetPlayedItem(userID, itemID, played); err != nil {
		errs = append(errs, err)
	}

	var kind string
	if err := r.db.QueryRow("SELECT kind FROM items WHERE id=?", itemID).Scan(&kind); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			errs = append(errs, err)
		}
	} else if kind == "Series" {
		_, err := r.db.Exec(`INSERT INTO userdata(user_id,item,played,position)
			SELECT ?, id, ?, 0 FROM items
			WHERE kind='Episode' AND (parent=? OR parent IN (SELECT id FROM items WHERE parent=? AND kind='Season'))
			ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0`, userID, playedValue, itemID, itemID)
		if err != nil {
			errs = append(errs, err)
		}
	} else if kind == "Season" {
		_, err := r.db.Exec(`INSERT INTO userdata(user_id,item,played,position)
			SELECT ?, id, ?, 0 FROM items
			WHERE kind='Episode' AND parent=?
			ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0`, userID, playedValue, itemID)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if !played {
		if _, err := r.db.Exec("DELETE FROM resume_activity WHERE user_id=? AND item=?", userID, itemID); err != nil {
			errs = append(errs, err)
		}
		if _, err := r.db.Exec("DELETE FROM plays WHERE user_id=? AND item=?", userID, itemID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r sqlUserRepository) SetPlaybackPosition(userID, itemID string, ticks int64) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.Exec("INSERT INTO userdata(user_id,item,played,position) VALUES(?,?,0,?) ON CONFLICT(user_id,item) DO UPDATE SET position=excluded.position", userID, itemID, ticks)
	return err
}

func (a *App) userStore() UserRepository {
	if a.userRepository != nil {
		return a.userRepository
	}
	return sqlUserRepository{db: a.db}
}
