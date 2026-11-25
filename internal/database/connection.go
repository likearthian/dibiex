package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"cloud.google.com/go/bigquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	bqapi "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/transport"
)

type Engine string

const (
	SQLite     Engine = "SQLite"
	PostgreSQL Engine = "PostgreSQL"
	MySQL      Engine = "MySQL"
	BigQuery   Engine = "BigQuery"
)

type Config struct {
	Engine                                        Engine
	Path                                          string
	Host, Port, Database, User, Password, SSLMode string
	Project, DatasetProject, Dataset, Location    string
	MaxBytesBilled                                int64
	BigQueryAuth, CredentialFile                  string          `json:",omitempty"`
	AdditionalProjects                            []string        `json:",omitempty"`
	HiddenProjects                                []string        `json:",omitempty"`
	ExplorerHiddenProjects                        map[string]bool `json:",omitempty"`
	ExplorerHiddenSchemas                         map[string]bool `json:",omitempty"`
}

type Table struct {
	Project, Schema, Name, Kind string
	Partition                   PartitionSpec
	Clustering                  []string
}

func (t Table) ID() string { return t.Project + "\x00" + t.Schema + "\x00" + t.Name }

func (t Table) Label() string {
	if t.Schema != "" && t.Schema != "main" {
		return t.Schema + "." + t.Name
	}
	return t.Name
}
func (t Table) Preview(engine Engine) string {
	return "SELECT * FROM " + t.Reference(engine) + " LIMIT 1000;"
}

func (t Table) Reference(engine Engine) string {
	ref := QuoteIdentifierFor(engine, t.Name)
	if engine == BigQuery {
		ref = "`" + strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(t.Project+"."+t.Schema+"."+t.Name) + "`"
	} else if t.Schema != "" {
		ref = QuoteIdentifierFor(engine, t.Schema) + "." + ref
	}
	return ref
}

// QuoteIdentifierFor quotes an identifier with the engine's native quoting.
func QuoteIdentifierFor(engine Engine, name string) string {
	if engine == MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return QuoteIdentifier(name)
}
func (cfg Config) Validate() error {
	switch cfg.Engine {
	case SQLite:
		if strings.TrimSpace(cfg.Path) == "" {
			return fmt.Errorf("choose a SQLite database file, or use New file to create one")
		}
	case PostgreSQL:
		if strings.TrimSpace(cfg.Host) == "" || strings.TrimSpace(cfg.Database) == "" || strings.TrimSpace(cfg.User) == "" {
			return fmt.Errorf("enter PostgreSQL host, database, and user")
		}
		port, err := strconv.Atoi(cfg.Port)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("enter a port between 1 and 65535")
		}
		switch cfg.SSLMode {
		case "verify-full", "verify-ca", "require", "prefer", "disable":
		default:
			return fmt.Errorf("choose a PostgreSQL SSL mode")
		}
	case BigQuery:
		if err := cfg.validateBigQueryAuth(); err != nil {
			return err
		}
		if cfg.MaxBytesBilled <= 0 {
			return fmt.Errorf("maximum bytes billed must be greater than zero")
		}
		if len(cfg.BigQueryProjects()) > MaxBigQueryProjects {
			return fmt.Errorf("a connection supports up to %d projects", MaxBigQueryProjects)
		}
		for _, project := range cfg.AdditionalProjects {
			if err := ValidateBigQueryProject(project); err != nil {
				return err
			}
		}
	case MySQL:
		if strings.TrimSpace(cfg.Host) == "" || strings.TrimSpace(cfg.Database) == "" || strings.TrimSpace(cfg.User) == "" {
			return fmt.Errorf("enter MySQL host, database, and user")
		}
		port, err := strconv.Atoi(cfg.Port)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("enter a port between 1 and 65535")
		}
		switch cfg.SSLMode {
		case "true", "preferred", "skip-verify", "false":
		default:
			return fmt.Errorf("choose a MySQL TLS mode")
		}
	default:
		return fmt.Errorf("choose a database engine")
	}
	return nil
}
func (cfg Config) Label() string {
	switch cfg.Engine {
	case PostgreSQL, MySQL:
		return cfg.Database + " @ " + cfg.Host
	case BigQuery:
		if project := cfg.dataProject(); strings.TrimSpace(project) != "" {
			return project
		}
		return string(BigQuery)
	default:
		return filepath.Base(cfg.Path)
	}
}
func (cfg Config) dataProject() string {
	if cfg.DatasetProject != "" {
		return cfg.DatasetProject
	}
	return cfg.Project
}
func (cfg Config) postgresURL() string {
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(cfg.Host, cfg.Port), Path: "/" + cfg.Database, User: url.UserPassword(cfg.User, cfg.Password)}
	q := url.Values{"sslmode": {cfg.SSLMode}, "application_name": {"dibiex"}}
	u.RawQuery = q.Encode()
	return u.String()
}
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Engine {
	case SQLite:
		return Open(ctx, cfg.Path)
	case PostgreSQL:
		parsed, err := pgx.ParseConfig(cfg.postgresURL())
		if err != nil {
			return nil, fmt.Errorf("invalid PostgreSQL connection settings")
		}
		parsed.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		db := stdlib.OpenDB(*parsed)
		db.SetMaxOpenConns(MaxQuerySessions + 1)
		db.SetMaxIdleConns(1)
		if err = db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("connect PostgreSQL: %w", err)
		}
		cfg.Password = ""
		conn, err := db.Conn(ctx)
		if err != nil {
			db.Close()
			return nil, err
		}
		return &Client{db: db, session: conn, config: cfg}, nil
	case MySQL:
		db, err := sql.Open("mysql", cfg.mysqlDSN())
		if err != nil {
			return nil, fmt.Errorf("open MySQL: %w", err)
		}
		db.SetMaxOpenConns(MaxQuerySessions + 1)
		db.SetMaxIdleConns(1)
		if err = db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("connect MySQL: %w", err)
		}
		cfg.Password = ""
		conn, err := db.Conn(ctx)
		if err != nil {
			db.Close()
			return nil, err
		}
		return &Client{db: db, session: conn, config: cfg}, nil
	case BigQuery:
		opts, err := bigQueryAuthOptions(ctx, cfg)
		if err != nil {
			return nil, err
		}
		project := strings.TrimSpace(cfg.Project)
		detectProject := project == ""
		if detectProject {
			project = bigquery.DetectProjectID
			if cfg.BigQueryAuthMethod() == BigQueryServiceAccount {
				credentials, err := transport.Creds(ctx, opts...)
				if err != nil {
					return nil, fmt.Errorf("BigQuery credentials: %w", err)
				}
				project = strings.TrimSpace(credentials.ProjectID)
				if project == "" {
					return nil, fmt.Errorf("service account key has no project ID; enter a BigQuery billing project")
				}
			}
		}
		bq, err := bigquery.NewClient(ctx, project, opts...)
		if err != nil {
			if detectProject {
				return nil, fmt.Errorf("could not detect the BigQuery billing project; enter a billing project ID or check the credentials: %w", err)
			}
			return nil, fmt.Errorf("BigQuery credentials: %w", err)
		}
		cfg.Project = bq.Project()
		if err := cfg.Validate(); err != nil {
			bq.Close()
			return nil, err
		}
		bq.Location = cfg.Location
		api, err := bqapi.NewService(ctx, opts...)
		if err != nil {
			bq.Close()
			return nil, fmt.Errorf("BigQuery metadata client: %w", err)
		}
		if cfg.Dataset != "" {
			if _, err = bq.DatasetInProject(cfg.dataProject(), cfg.Dataset).Metadata(ctx); err != nil {
				bq.Close()
				return nil, fmt.Errorf("connect BigQuery dataset: %w", err)
			}
		} else if _, err = api.Datasets.List(cfg.dataProject()).MaxResults(1).Context(ctx).Do(); err != nil {
			bq.Close()
			return nil, fmt.Errorf("connect BigQuery project: %w", err)
		}
		return &Client{bq: bq, bqAPI: api, config: cfg}, nil
	}
	return nil, fmt.Errorf("unsupported database engine")
}
func (c *Client) Engine() Engine {
	if c == nil || c.config.Engine == "" {
		return SQLite
	}
	return c.config.Engine
}

func (c *Client) Settings() Config {
	if c == nil {
		return Config{Engine: SQLite}
	}
	cfg := c.config
	cfg.Password = ""
	return cfg
}
func (c *Client) Scope() string {
	if c == nil {
		return "main"
	}
	switch c.Engine() {
	case PostgreSQL, MySQL:
		return c.config.Database
	case BigQuery:
		if c.config.Dataset == "" {
			return c.config.dataProject()
		}
		return c.config.Dataset
	default:
		return "main"
	}
}
func (c *Client) WelcomeQuery() string {
	switch c.Engine() {
	case PostgreSQL:
		return "SELECT current_database(), current_user, version();"
	case MySQL:
		return "SELECT DATABASE() AS database_name, CURRENT_USER() AS user, VERSION() AS version;"
	case BigQuery:
		return "SELECT CURRENT_TIMESTAMP() AS connected_at;"
	default:
		return "SELECT name, type FROM sqlite_schema ORDER BY name;"
	}
}
func (c *Client) Objects(ctx context.Context) ([]Table, error) {
	if c.bq != nil {
		if c.config.Dataset != "" {
			return c.SchemaTables(ctx, Schema{Project: c.config.dataProject(), Name: c.config.Dataset})
		}
		schemas, err := c.Catalog(ctx)
		if err != nil {
			return nil, err
		}
		var out []Table
		for _, schema := range schemas {
			tables, err := c.SchemaTables(ctx, schema)
			if err != nil {
				return nil, err
			}
			out = append(out, tables...)
			if len(out) > CatalogLimit {
				return nil, fmt.Errorf("catalog exceeds the 10,000 object display limit")
			}
		}
		return out, nil
	}
	if c.Engine() == PostgreSQL {
		rows, err := c.catalogQuery(ctx, `SELECT n.nspname, c.relname, c.relkind::text, COALESCE(pg_get_partkeydef(c.oid),'') FROM pg_catalog.pg_class c
 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
 AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%'
 ORDER BY n.nspname, c.relname`)
		if err != nil {
			return nil, err
		}
		if rows.Truncated {
			return nil, fmt.Errorf("catalog exceeds the 10,000 object display limit")
		}
		tables := make([]Table, 0, len(rows.Rows))
		for _, row := range rows.Rows {
			kind := "Tables"
			switch row[2] {
			case "p":
				kind = "Partitioned tables"
			case "v":
				kind = "Views"
			case "m":
				kind = "Materialized views"
			case "f":
				kind = "Foreign tables"
			}
			tables = append(tables, Table{Schema: row[0], Name: row[1], Kind: kind, Partition: PartitionSpec{Expression: row[3]}})
		}
		return tables, nil
	}
	if c.Engine() == MySQL {
		rows, err := c.catalogQuery(ctx, `SELECT table_schema, table_name,
 CASE WHEN table_type='VIEW' THEN 'Views' ELSE 'Tables' END
 FROM information_schema.tables
 WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')
 ORDER BY table_schema, table_name`)
		if err != nil {
			return nil, err
		}
		if rows.Truncated {
			return nil, fmt.Errorf("catalog exceeds the 10,000 object display limit")
		}
		tables := make([]Table, 0, len(rows.Rows))
		for _, row := range rows.Rows {
			tables = append(tables, Table{Schema: row[0], Name: row[1], Kind: row[2]})
		}
		return tables, nil
	}
	schemas, err := c.catalogQuery(ctx, "SELECT name FROM pragma_database_list ORDER BY seq")
	if err != nil {
		return nil, err
	}
	var tables []Table
	for _, schema := range schemas.Rows {
		rows, err := c.catalogQuery(ctx, "SELECT name,type FROM "+QuoteIdentifier(schema[0])+".sqlite_schema WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name")
		if err != nil {
			return nil, err
		}
		for _, row := range rows.Rows {
			kind := "Tables"
			if row[1] == "view" {
				kind = "Views"
			}
			tables = append(tables, Table{Schema: schema[0], Name: row[0], Kind: kind})
			if len(tables) > CatalogLimit {
				return nil, fmt.Errorf("catalog exceeds 10,000 objects")
			}
		}
	}
	return tables, nil
}
