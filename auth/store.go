package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrEmailTaken is returned when signing up (or first signing in with a
	// provider) with an email that already belongs to another customer.
	ErrEmailTaken = errors.New("an account with this email already exists")
	// ErrInvalidCredentials is returned for an unknown email, a wrong
	// password, or a customer who has no password set.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrInvalidSession is returned for an unknown or expired token.
	ErrInvalidSession = errors.New("invalid or expired session")
)

// Store persists customers, identities and sessions in Postgres.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: time.Now}
}

const customerColumns = `c.id, c.email, c.created_at`

func scanCustomer(row pgx.Row, extra ...any) (Customer, error) {
	var c Customer
	return c, row.Scan(append([]any{&c.ID, &c.Email, &c.CreatedAt}, extra...)...)
}

// SignUp creates a customer with a password and starts a session.
func (s *Store) SignUp(ctx context.Context, email, password string) (Session, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return Session{}, err
	}
	var sess Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := scanCustomer(tx.QueryRow(ctx, `
			INSERT INTO customers AS c (id, email, password_hash) VALUES ($1, $2, $3)
			RETURNING `+customerColumns,
			rand.Text(), email, hash))
		if isUniqueViolation(err, "customers_email_key") {
			return ErrEmailTaken
		}
		if err != nil {
			return err
		}
		sess, err = s.createSession(ctx, tx, c)
		return err
	})
	return sess, err
}

// LogIn checks an email and password and starts a session.
func (s *Store) LogIn(ctx context.Context, email, password string) (Session, error) {
	var hash *string
	c, err := scanCustomer(s.pool.QueryRow(ctx,
		`SELECT `+customerColumns+`, c.password_hash FROM customers c WHERE c.email = $1`, email), &hash)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && hash == nil) {
		verifyPassword(password, dummyHash) // equalize timing
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	ok, err := verifyPassword(password, *hash)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, ErrInvalidCredentials
	}
	return s.createSession(ctx, s.pool, c)
}

// SignInWithIdentity starts a session for the customer linked to id, creating
// the customer on first sign-in. created reports whether it did.
//
// A new identity is never linked to an existing customer by email: that
// would let whoever registered an email first (password signups are not
// email-verified) take over a later Apple/Google sign-in. Such attempts get
// ErrEmailTaken instead.
func (s *Store) SignInWithIdentity(ctx context.Context, id Identity) (sess Session, created bool, err error) {
	for attempt := 0; ; attempt++ {
		sess, created, err = s.signInWithIdentity(ctx, id)
		// A concurrent first sign-in for the same identity won the insert;
		// retry once to pick up the customer it created.
		if isUniqueViolation(err, "customer_identities_pkey") && attempt == 0 {
			continue
		}
		return sess, created, err
	}
}

func (s *Store) signInWithIdentity(ctx context.Context, id Identity) (sess Session, created bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := scanCustomer(tx.QueryRow(ctx, `
			SELECT `+customerColumns+` FROM customers c
			JOIN customer_identities i ON i.customer_id = c.id
			WHERE i.provider = $1 AND i.subject = $2`,
			id.Provider, id.Subject))
		if errors.Is(err, pgx.ErrNoRows) {
			var email *string
			if id.EmailVerified {
				email = &id.Email
			}
			c, err = scanCustomer(tx.QueryRow(ctx,
				`INSERT INTO customers AS c (id, email) VALUES ($1, $2) RETURNING `+customerColumns,
				rand.Text(), email))
			if isUniqueViolation(err, "customers_email_key") {
				return ErrEmailTaken
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_identities (provider, subject, customer_id) VALUES ($1, $2, $3)`,
				id.Provider, id.Subject, c.ID); err != nil {
				return err
			}
			created = true
		} else if err != nil {
			return err
		}
		sess, err = s.createSession(ctx, tx, c)
		return err
	})
	return sess, created, err
}

// CustomerForToken returns the customer that owns an unexpired session token.
func (s *Store) CustomerForToken(ctx context.Context, token string) (Customer, error) {
	c, err := scanCustomer(s.pool.QueryRow(ctx, `
		SELECT `+customerColumns+` FROM sessions s
		JOIN customers c ON c.id = s.customer_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`,
		hashToken(token), s.now()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrInvalidSession
	}
	return c, err
}

// LogOut deletes the session for token. Unknown tokens are not an error.
func (s *Store) LogOut(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

type execQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// createSession issues a new token for c and prunes c's expired sessions.
func (s *Store) createSession(ctx context.Context, db execQuerier, c Customer) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	expires := now.Add(SessionTTL).UTC().Truncate(time.Millisecond)

	if _, err := db.Exec(ctx, `DELETE FROM sessions WHERE customer_id = $1 AND expires_at <= $2`, c.ID, now); err != nil {
		return Session{}, err
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO sessions (token_hash, customer_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), c.ID, expires); err != nil {
		return Session{}, err
	}
	return Session{Token: token, ExpiresAt: expires, Customer: c}, nil
}

// hashToken is what sessions.token_hash stores. Tokens carry 256 bits of
// randomness, so a fast unsalted hash is enough.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
