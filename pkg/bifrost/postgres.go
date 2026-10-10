package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	gatewayclient "github.com/obot-platform/obot/pkg/gateway/client"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
)

type credentialKeyStore struct {
	ctx    context.Context
	client *gatewayclient.Client
}

// DerivePostgresDSN returns a DSN for a database named after Obot's own, on the same Postgres
// server, creating the database if it doesn't exist. Bifrost's tables can't share Obot's
// database, but sharing the server keeps every Obot replica on the same gateway state
// without extra configuration.
func DerivePostgresDSN(ctx context.Context, obotDSN string) (string, error) {
	u, err := url.Parse(strings.Replace(obotDSN, "postgresql://", "postgres://", 1))
	if err != nil {
		return "", fmt.Errorf("invalid Postgres DSN: %w", err)
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		dbName = "obot"
	}
	gatewayDB := dbName + "_llm_gateway"

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return "", fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	defer conn.Close(context.Background())

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", gatewayDB).Scan(&exists); err != nil {
		return "", fmt.Errorf("failed to look up database %q: %w", gatewayDB, err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{gatewayDB}.Sanitize()); err != nil {
			// Another replica may have created it at the same time.
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42P04" {
				return "", fmt.Errorf("failed to create database %q: %w", gatewayDB, err)
			}
		}
	}

	u.Path = "/" + gatewayDB
	return u.String(), nil
}

// CredentialKeyStore keeps the shared encryption key in Obot's credential store, which is
// encrypted with Obot's own encryption configuration when one is set.
func CredentialKeyStore(ctx context.Context, c *gatewayclient.Client) SharedKeyStore {
	return credentialKeyStore{ctx: ctx, client: c}
}

func (s credentialKeyStore) EnsureEncryptionKey(key string) (string, error) {
	cred, err := s.client.EnsureCredential(s.ctx, gatewaytypes.Credential{
		Context: "llm-gateway",
		Name:    "encryption-key",
		Secrets: map[string]string{"key": key},
	})
	if err != nil {
		return "", err
	}
	if cred.Secrets["key"] == "" {
		return "", errors.New("the stored LLM gateway encryption key is empty")
	}
	return cred.Secrets["key"], nil
}
