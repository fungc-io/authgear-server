package pq

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/authgear/authgear-server/pkg/lib/infra/db"
	"github.com/authgear/authgear-server/pkg/lib/infra/db/appdb"
	"github.com/authgear/authgear-server/pkg/lib/oauth"
)

type AuthorizationStore struct {
	SQLBuilder  *appdb.SQLBuilderApp
	SQLExecutor *appdb.SQLExecutor
}

func (s *AuthorizationStore) selectQuery() db.SelectBuilder {
	return s.SQLBuilder.Select(
		"id",
		"app_id",
		"client_id",
		"user_id",
		"created_at",
		"updated_at",
		"scopes",
	).
		From(s.SQLBuilder.TableName("_auth_oauth_authorization"))
}

func (s *AuthorizationStore) Get(ctx context.Context, userID, clientID string) (*oauth.Authorization, error) {
	builder := s.selectQuery().
		Where("user_id = ? AND client_id = ?", userID, clientID)

	scanner, err := s.SQLExecutor.QueryRowWith(ctx, builder)
	if err != nil {
		return nil, err
	}

	return s.scanAuthz(scanner)
}

func (s *AuthorizationStore) GetByID(ctx context.Context, id string) (*oauth.Authorization, error) {
	builder := s.selectQuery().
		Where("id = ?", id)

	scanner, err := s.SQLExecutor.QueryRowWith(ctx, builder)
	if err != nil {
		return nil, err
	}

	return s.scanAuthz(scanner)
}

func (s *AuthorizationStore) ListByUserID(ctx context.Context, userID string) ([]*oauth.Authorization, error) {
	builder := s.selectQuery().
		Where("user_id = ?", userID)

	rows, err := s.SQLExecutor.QueryWith(ctx, builder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var as []*oauth.Authorization
	for rows.Next() {
		a, err := s.scanAuthz(rows)
		if err != nil {
			return nil, err
		}
		as = append(as, a)
	}
	return as, nil
}

func (s *AuthorizationStore) scanAuthz(scn db.Scanner) (*oauth.Authorization, error) {
	authz := &oauth.Authorization{}

	var scopeBytes []byte

	err := scn.Scan(
		&authz.ID,
		&authz.AppID,
		&authz.ClientID,
		&authz.UserID,
		&authz.CreatedAt,
		&authz.UpdatedAt,
		&scopeBytes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, oauth.ErrAuthorizationNotFound
	} else if err != nil {
		return nil, err
	}

	err = json.Unmarshal(scopeBytes, &authz.Scopes)
	if err != nil {
		return nil, err
	}

	return authz, nil
}

func (s *AuthorizationStore) Create(ctx context.Context, authz *oauth.Authorization) error {
	scopeBytes, err := json.Marshal(authz.Scopes)
	if err != nil {
		return err
	}

	builder := s.SQLBuilder.
		Insert(s.SQLBuilder.TableName("_auth_oauth_authorization")).
		Columns(
			"id",
			"client_id",
			"user_id",
			"created_at",
			"updated_at",
			"scopes",
		).
		Values(
			authz.ID,
			authz.ClientID,
			authz.UserID,
			authz.CreatedAt,
			authz.UpdatedAt,
			scopeBytes,
		)

	_, err = s.SQLExecutor.ExecWith(ctx, builder)
	if err != nil {
		return err
	}

	return nil
}

func (s *AuthorizationStore) Delete(ctx context.Context, authz *oauth.Authorization) error {
	builder := s.SQLBuilder.
		Delete(s.SQLBuilder.TableName("_auth_oauth_authorization")).
		Where("id = ?", authz.ID)

	_, err := s.SQLExecutor.ExecWith(ctx, builder)
	if err != nil {
		return err
	}

	return nil
}

func (s *AuthorizationStore) ResetAll(ctx context.Context, userID string) error {
	builder := s.SQLBuilder.
		Delete(s.SQLBuilder.TableName("_auth_oauth_authorization")).
		Where("user_id = ?", userID)

	_, err := s.SQLExecutor.ExecWith(ctx, builder)
	if err != nil {
		return err
	}

	return nil
}

func (s *AuthorizationStore) UpdateScopes(ctx context.Context, authz *oauth.Authorization) error {
	scopeBytes, err := json.Marshal(authz.Scopes)
	if err != nil {
		return err
	}

	builder := s.SQLBuilder.
		Update(s.SQLBuilder.TableName("_auth_oauth_authorization")).
		Set("updated_at", authz.UpdatedAt).
		Set("scopes", scopeBytes).
		Where("id = ?", authz.ID)

	_, err = s.SQLExecutor.ExecWith(ctx, builder)
	if err != nil {
		return err
	}

	return nil
}
