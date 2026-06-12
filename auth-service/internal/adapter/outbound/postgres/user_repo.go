package postgres

import (
	"context"
	"database/sql"
	"errors"

	"go-flashsale-mini-kafka-basic/auth-service/internal/application/domain"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) CreateUser(ctx context.Context, user *domain.User) error {
	query := `INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3)`
	_, err := r.db.ExecContext(ctx, query, user.ID, user.Username, user.PasswordHash)
	return err
}

func (r *UserRepository) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `SELECT id, username, password_hash FROM users WHERE username = $1`
	row := r.db.QueryRowContext(ctx, query, username)

	var user domain.User
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Not found
		}
		return nil, err
	}
	return &user, nil
}
