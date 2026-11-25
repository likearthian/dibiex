package database

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const MaxTransferBytes int64 = 256 << 20

type PGToolOptions struct {
	Restore, DataOnly, Clean, Plain, SchemaOnly bool
	Tool, Path, Password                        string
}

func PGToolArguments(cfg Config, t Table, o PGToolOptions) (string, []string, error) {
	if cfg.Engine != PostgreSQL {
		return "", nil, fmt.Errorf("PostgreSQL tools require a PostgreSQL connection")
	}
	if err := cfg.Validate(); err != nil {
		return "", nil, err
	}
	if err := pgIdentifier(t.Schema, "Schema"); err != nil {
		return "", nil, err
	}
	if err := pgIdentifier(t.Name, "Table"); err != nil {
		return "", nil, err
	}
	u := url.URL{Scheme: "postgresql", Host: net.JoinHostPort(cfg.Host, cfg.Port), User: url.User(cfg.User), Path: "/" + cfg.Database, RawPath: "/" + url.PathEscape(cfg.Database), RawQuery: url.Values{"sslmode": {cfg.SSLMode}}.Encode()}
	args := []string{"--no-password", "--dbname=" + u.String()}
	if o.Restore {
		args = append(args, "--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--schema="+t.Schema, "--table="+t.Name)
		if o.DataOnly {
			args = append(args, "--data-only")
		} else if o.Clean {
			args = append(args, "--clean", "--if-exists")
		}
		return "pg_restore", append(args, "--", o.Path), nil
	}
	format := "custom"
	if o.Plain {
		format = "plain"
	}
	args = append(args, "--format="+format, "--table="+t.Reference(PostgreSQL))
	if o.SchemaOnly {
		args = append(args, "--schema-only")
	}
	return "pg_dump", args, nil
}

type LimitedWriter struct {
	Writer    io.Writer
	Remaining int64
	Cancel    context.CancelFunc
}

func (w *LimitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		if w.Cancel != nil {
			w.Cancel()
		}
		return 0, fmt.Errorf("transfer exceeds the 256 MiB limit")
	}
	n, err := w.Writer.Write(p)
	w.Remaining -= int64(n)
	return n, err
}

type toolLog struct{ data []byte }

func (w *toolLog) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - len(w.data)
	if remaining > 0 {
		w.data = append(w.data, p[:min(remaining, len(p))]...)
	}
	return n, nil
}

func RunPostgreSQLTool(ctx context.Context, cfg Config, t Table, o PGToolOptions) error {
	name, args, err := PGToolArguments(cfg, t, o)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Path) == "" {
		return fmt.Errorf("choose an archive file")
	}
	tool := o.Tool
	if tool == "" {
		tool = name
	}
	tool, err = exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s not found; install matching PostgreSQL client tools or select the executable", name)
	}
	if o.Restore {
		info, err := os.Stat(o.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxTransferBytes {
			return fmt.Errorf("choose a regular archive file no larger than 256 MiB")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	hidePostgreSQLTool(cmd)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PGPASSWORD=") && !strings.HasPrefix(entry, "PGSERVICE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PGPASSWORD="+o.Password, "PGAPPNAME=dibiex", "PGCONNECT_TIMEOUT=15")
	log := &toolLog{}
	cmd.Stderr = log
	var temporary *os.File
	if !o.Restore {
		temporary, err = os.CreateTemp(filepath.Dir(o.Path), "dbcraft-backup-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(temporary.Name())
		cmd.Stdout = &LimitedWriter{Writer: temporary, Remaining: MaxTransferBytes, Cancel: cancel}
	}
	err = cmd.Run()
	if temporary != nil {
		closeErr := temporary.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		message := string(log.data)
		if o.Password != "" {
			message = strings.ReplaceAll(message, o.Password, "[redacted]")
		}
		return fmt.Errorf("%s failed: %s (%w)", name, strings.TrimSpace(message), err)
	}
	if temporary != nil {
		return os.Rename(temporary.Name(), o.Path)
	}
	return nil
}
