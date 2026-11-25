package database

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

const (
	BigQueryADC            = "Application Default Credential"
	BigQueryServiceAccount = "Service account file"
)

func (cfg Config) BigQueryAuthMethod() string {
	if cfg.BigQueryAuth == "" || cfg.BigQueryAuth == "Existing ADC" {
		return BigQueryADC
	}
	return cfg.BigQueryAuth
}

func (cfg Config) validateBigQueryAuth() error {
	switch cfg.BigQueryAuthMethod() {
	case BigQueryADC:
	case BigQueryServiceAccount:
		if cfg.CredentialFile == "" {
			return errors.New("choose a service account JSON credential file")
		}
	default:
		return errors.New("unsupported BigQuery authentication method")
	}
	return nil
}

func readAuthFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open Google credential file; check its path and permissions")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("Google credential file is unreadable or exceeds 1 MiB")
	}
	return data, nil
}

func bigQueryAuthOptions(ctx context.Context, cfg Config) ([]option.ClientOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.validateBigQueryAuth(); err != nil {
		return nil, err
	}
	switch cfg.BigQueryAuthMethod() {
	case BigQueryServiceAccount:
		data, err := readAuthFile(cfg.CredentialFile)
		if err != nil {
			return nil, err
		}
		var header struct {
			Type      string `json:"type"`
			ProjectID string `json:"project_id"`
		}
		if json.Unmarshal(data, &header) != nil || header.Type != "service_account" {
			return nil, errors.New("choose a valid service_account JSON key file")
		}
		// Parse only the selected key type; never execute external credential configurations.
		jwt, err := google.JWTConfigFromJSON(data, "https://www.googleapis.com/auth/bigquery")
		if err != nil {
			return nil, errors.New("invalid service account JSON key")
		}
		if jwt.TokenURL != google.JWTTokenURL {
			return nil, errors.New("service account key must use Google's token endpoint")
		}
		if jwt.Email == "" || len(jwt.PrivateKey) == 0 {
			return nil, errors.New("service account key is missing its email or private key")
		}
		return []option.ClientOption{option.WithCredentials(&google.Credentials{
			ProjectID: header.ProjectID, TokenSource: jwt.TokenSource(authRefreshContext()),
		})}, nil
	default:
		return nil, nil
	}
}

func authRefreshContext() context.Context {
	// Refresh must outlive the short connection/sign-in operation, with bounded IO.
	return context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
}
