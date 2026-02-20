package store

import "database/sql"

type LoginCode struct {
	Code        string
	UserID      string
	CallbackURL string
	ExpiresAt   string
	Used        bool
}

func (s *Store) CreateLoginCode(code, userID, callbackURL, expiresAt string) error {
	_, err := s.db.Exec(
		"INSERT INTO login_codes (code, user_id, callback_url, expires_at) VALUES (?, ?, ?, ?)",
		code, userID, callbackURL, expiresAt,
	)
	return err
}

func (s *Store) GetLoginCode(code string) (*LoginCode, error) {
	lc := &LoginCode{}
	var used int
	err := s.db.QueryRow(`
		SELECT code, user_id, callback_url, expires_at, used
		FROM login_codes
		WHERE code = ? AND used = 0 AND expires_at > strftime('%Y-%m-%d %H:%M:%S', 'now')`,
		code,
	).Scan(&lc.Code, &lc.UserID, &lc.CallbackURL, &lc.ExpiresAt, &used)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lc.Used = used != 0
	return lc, err
}

func (s *Store) MarkLoginCodeUsed(code string) error {
	_, err := s.db.Exec("UPDATE login_codes SET used = 1 WHERE code = ?", code)
	return err
}
