package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// tokenPrefix starts every API token, so one pasted somewhere it shouldn't
// be is easy to spot.
const tokenPrefix = "md_"

// TokenApp is the one token kind so far: another church app reading the
// directory (the event planner's sign-ups, step 14 of PLAN.md).
const TokenApp = "app"

// Token lets another site or app use the API. The token itself is only
// known when it's made; afterwards it's Prefix, its first characters.
type Token struct {
	ID        int64
	Name      string // what uses it, e.g. "Event planner"
	Kind      string
	Prefix    string
	CreatedBy string
	CreatedAt string
	LastUsed  string // "" if never
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateToken makes a token of kind (TokenApp) and returns
// it. This is the only time it can be seen.
func (s *Store) CreateToken(name, kind string, createdBy int64) (string, error) {
	token := tokenPrefix + RandomToken()
	_, err := s.DB.Exec(`INSERT INTO api_tokens (name, kind, hash, prefix, created_by) VALUES (?, ?, ?, ?, ?)`,
		strings.TrimSpace(name), kind, hashToken(token), token[:len(tokenPrefix)+6], nullInt(createdBy))
	return token, err
}

// ListTokens lists the tokens of one kind by name.
func (s *Store) ListTokens(kind string) ([]Token, error) {
	rows, err := s.DB.Query(`
		SELECT t.id, t.name, t.kind, t.prefix,
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), t.created_at, COALESCE(t.last_used, '')
		FROM api_tokens t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.kind = ? ORDER BY t.name COLLATE NOCASE, t.id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.Prefix,
			&t.CreatedBy, &t.CreatedAt, &t.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken revokes a token: it stops working at once.
func (s *Store) DeleteToken(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

// AuthenticateToken finds the token and notes that it was used (at most
// once a minute, to spare the database a write on every request). Unknown
// tokens give ErrNotFound.
func (s *Store) AuthenticateToken(raw string) (*Token, error) {
	if !strings.HasPrefix(raw, tokenPrefix) {
		return nil, ErrNotFound
	}
	var t Token
	if err := s.DB.QueryRow(`SELECT id, name, kind, prefix FROM api_tokens WHERE hash = ?`,
		hashToken(raw)).Scan(&t.ID, &t.Name, &t.Kind, &t.Prefix); err != nil {
		return nil, notFound(err)
	}
	_, err := s.DB.Exec(`UPDATE api_tokens SET last_used = datetime('now')
		WHERE id = ? AND (last_used IS NULL OR last_used < datetime('now', '-1 minute'))`, t.ID)
	return &t, err
}
