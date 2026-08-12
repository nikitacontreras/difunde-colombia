// Command server: API de sonda de conectividad para Colombia Difunde.
//
// Subcomandos:
//
//	server serve            (default) arranca la API y sirve el frontend
//	server import-data DIR  importa datasets oficiales (baseline)
//	server load-mapping CSV carga mappings ASN -> operador verificados
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"colombia-difunde/internal/asn"
	"colombia-difunde/internal/config"
	"colombia-difunde/internal/db"
	"colombia-difunde/internal/geo"
	"colombia-difunde/internal/importer"
	"colombia-difunde/internal/observe"
	"colombia-difunde/internal/server"
	"colombia-difunde/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	sub := ""
	if len(os.Args) > 1 {
		sub = os.Args[1]
	}
	switch sub {
	case "import-data":
		dir := "./data"
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		return runImport(cfg, dir)
	case "load-mapping":
		path := cfg.AsnMappingCSV
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if path == "" {
			return fmt.Errorf("uso: server load-mapping <asn_operator_mapping.csv>")
		}
		return runLoadMapping(cfg, path)
	default:
		return runServe(cfg)
	}
}

func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var lastErr error
	for i := 0; i < 30; i++ {
		pool, lastErr = pgxpool.New(ctx, url)
		if lastErr == nil {
			if err := pool.Ping(ctx); err == nil {
				return pool, nil
			} else {
				pool.Close()
				lastErr = err
			}
		}
		slog.Warn("postgres no disponible, reintentando", "err", lastErr, "intento", i+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("postgres no disponible: %w", lastErr)
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	m := &db.Migrator{
		Exec: func(ctx context.Context, sql string, args ...any) error {
			_, err := pool.Exec(ctx, sql, args...)
			return err
		},
		QueryScalar: func(ctx context.Context, sql string, args ...any) (any, error) {
			rows, err := pool.Query(ctx, sql, args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []any
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			return out, rows.Err()
		},
	}
	return m.Apply(ctx)
}

func runServe(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := applyMigrations(ctx, pool); err != nil {
		return fmt.Errorf("migraciones: %w", err)
	}

	st, err := store.NewPGStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	var ar asn.Resolver = asn.EmptyResolver{}
	if cfg.AsnDB != "" {
		r, err := asn.NewCSVResolver(cfg.AsnDB)
		if err != nil {
			return fmt.Errorf("base ASN: %w", err)
		}
		ar = r
		slog.Info("base ASN cargada")
	}

	ops := observe.NewOperatorResolver()
	if cfg.AsnMappingCSV != "" {
		r, err := observe.LoadOperatorMappingsCSV(cfg.AsnMappingCSV)
		if err != nil {
			return err
		}
		ops = r
		slog.Info("mappings ASN->operador desde CSV", "n", ops.Size())
	} else if cfg.AsnMappingFromDB {
		rows, err := st.QueryMappings(ctx)
		if err != nil {
			return fmt.Errorf("mappings ASN desde DB: %w", err)
		}
		for _, row := range rows {
			ops.AddRow(observe.OperatorMappingRow{
				ASN: row.ASN, Operator: row.Operator, Mobile: row.Mobile,
				Confidence: row.Confidence, Source: row.Source,
			})
		}
		slog.Info("mappings ASN->operador desde DB", "n", ops.Size())
	}

	cr := geo.H3{Res: cfg.H3Resolution}
	slog.Info("resolución H3", "res", cfg.H3Resolution, "desc", geo.ResolutionDescription(cfg.H3Resolution))

	srv := server.New(cfg, st, ar, ops, cr)
	httpSrv := &httpServer{cfg: cfg, handler: srv.Handler()}
	return httpSrv.Run(ctx)
}

func runImport(cfg config.Config, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := applyMigrations(ctx, pool); err != nil {
		return fmt.Errorf("migraciones: %w", err)
	}

	rep, err := importer.ImportData(ctx, pool, abs, cfg.H3Resolution)
	if err != nil {
		return fmt.Errorf("importar: %w", err)
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	return nil
}

func runLoadMapping(cfg config.Config, path string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	r, err := observe.LoadOperatorMappingsCSV(path)
	if err != nil {
		return err
	}
	entries := r.Entries()
	for asn, m := range entries {
		if _, err := pool.Exec(ctx,
			`INSERT INTO asn_operator_mapping (asn, operator, mobile, confidence, source)
			 VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT (asn) DO UPDATE SET
			   operator=EXCLUDED.operator, mobile=EXCLUDED.mobile,
			   confidence=EXCLUDED.confidence, source=EXCLUDED.source`,
			asn, m.Operator, m.Mobile, m.Confidence, m.Source); err != nil {
			return err
		}
	}
	fmt.Printf("Cargados %d mappings ASN->operador\n", len(entries))
	return nil
}

type httpServer struct {
	cfg     config.Config
	handler http.Handler
}

func (h *httpServer) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:         ":" + h.cfg.Port,
		Handler:      h.handler,
		ReadTimeout:  h.cfg.HTTP.ReadTimeout,
		WriteTimeout: h.cfg.HTTP.WriteTimeout,
		IdleTimeout:  h.cfg.HTTP.IdleTimeout,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("servidor listo", "addr", srv.Addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shCtx)
	}
}
