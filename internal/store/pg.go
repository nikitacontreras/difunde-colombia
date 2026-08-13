package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"colombia-difunde/internal/observe"
)

// PGStore implementa Store sobre PostgreSQL + PostGIS.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(ctx context.Context, url string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

func (s *PGStore) Close() { s.pool.Close() }

func (s *PGStore) InsertObservation(ctx context.Context, o Observation) (int64, error) {
	query := `INSERT INTO observations (
		received_at, observed_at, latitude, longitude, accuracy, geom, h3_cell,
		asn, operator, mobile, http_rtt_min, http_rtt_median, jitter, success_ratio,
		samples, failed_requests, effective_type, browser_rtt, browser_downlink,
		save_data, call_signal, operator_user, probe_1k_ms, probe_4k_ms, transfer_estimate_bps
	) VALUES ($1,$2,$3,$4,$5, ST_SetSRID(ST_MakePoint($4,$3),4326), $6,
		$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, query,
		o.ReceivedAt, o.ObservedAt, o.Latitude, o.Longitude, o.Accuracy, o.H3Cell,
		o.ASN, o.Operator, o.Mobile, o.HttpRTTMin, o.HttpRTTMedian, o.Jitter, o.SuccessRatio,
		o.Samples, o.FailedRequests, nilStr(o.EffectiveType), o.BrowserRTT, o.BrowserDownlink,
		o.SaveData, nilStr(o.CallSignal), nilStr(o.OperatorUser), o.Probe1kMs, o.Probe4kMs, o.TransferEstimateBps,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *PGStore) InsertObservations(ctx context.Context, obs []Observation) error {
	if len(obs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, o := range obs {
		query := `INSERT INTO observations (
			received_at, observed_at, latitude, longitude, accuracy, geom, h3_cell,
			asn, operator, mobile, http_rtt_min, http_rtt_median, jitter, success_ratio,
			samples, failed_requests, effective_type, browser_rtt, browser_downlink,
			save_data, call_signal, operator_user, probe_1k_ms, probe_4k_ms, transfer_estimate_bps
		) VALUES ($1,$2,$3,$4,$5, ST_SetSRID(ST_MakePoint($4,$3),4326), $6,
			$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`
		batch.Queue(query,
			o.ReceivedAt, o.ObservedAt, o.Latitude, o.Longitude, o.Accuracy, o.H3Cell,
			o.ASN, o.Operator, o.Mobile, o.HttpRTTMin, o.HttpRTTMedian, o.Jitter, o.SuccessRatio,
			o.Samples, o.FailedRequests, nilStr(o.EffectiveType), o.BrowserRTT, o.BrowserDownlink,
			o.SaveData, nilStr(o.CallSignal), nilStr(o.OperatorUser), o.Probe1kMs, o.Probe4kMs, o.TransferEstimateBps)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(obs); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("observación %d/%d: %w", i, len(obs), err)
		}
	}
	return nil
}

func (s *PGStore) UpdateObservation(ctx context.Context, id int64, callSignal, operatorUser *string) error {
	query := `UPDATE observations SET
		call_signal = COALESCE($2, call_signal),
		operator_user = COALESCE($3, operator_user),
		operator = CASE
			WHEN $3 IS NOT NULL AND (operator IS NULL OR operator = 'desconocido')
			THEN $3 ELSE operator END
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, query, id, callSignal, operatorUser)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("observación %d no encontrada", id)
	}
	return nil
}

func (s *PGStore) Cells(ctx context.Context, f CellFilter) ([]CellAgg, error) {
	query := `SELECT h3_cell,
			count(*) AS n,
			COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY http_rtt_median), 0) AS rtt,
			COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY jitter), 0) AS jitter,
			COALESCE(avg(success_ratio), 0) AS q,
			mode() WITHIN GROUP (ORDER BY operator) AS op,
			max(observed_at) AS last,
			count(*) FILTER (WHERE call_signal = 'yes') AS cy,
			count(*) FILTER (WHERE call_signal = 'no') AS cn,
			count(*) FILTER (WHERE call_signal = 'unknown') AS cu,
			COALESCE(string_agg(DISTINCT effective_type, ','), '') AS ets
		FROM observations
		WHERE observed_at >= $1
		  AND geom && ST_MakeEnvelope($2,$3,$4,$5,4326)
		  AND ($6::text IS NULL OR operator = $6)
		GROUP BY h3_cell
		ORDER BY n DESC`
	var opFilter any
	if f.Operator != "" {
		opFilter = f.Operator
	}
	rows, err := s.pool.Query(ctx, query,
		nowMinus(f.Window), f.MinLon, f.MinLat, f.MaxLon, f.MaxLat, opFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CellAgg
	for rows.Next() {
		var c CellAgg
		var ets string
		if err := rows.Scan(&c.Cell, &c.Count, &c.MedianRTT, &c.MedianJitter, &c.SuccessRatio,
			&c.TopOperator, &c.LastObserved, &c.CallYes, &c.CallNo, &c.CallUnknown, &ets); err != nil {
			return nil, err
		}
		c.EffectiveType = map[string]int{}
		if ets != "" {
			for _, e := range strings.Split(ets, ",") {
				if e != "" {
					c.EffectiveType[e]++
				}
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) Sites(ctx context.Context, f CellFilter) ([]Site, error) {
	query := `SELECT latitude, longitude, COALESCE(operator,''), source,
		COALESCE(neighborhood,''), COALESCE(address,''), COALESCE(source_date,'')
		FROM mobile_sites
		WHERE geom && ST_MakeEnvelope($1,$2,$3,$4,4326)
		LIMIT 5000`
	rows, err := s.pool.Query(ctx, query, f.MinLon, f.MinLat, f.MaxLon, f.MaxLat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var st Site
		if err := rows.Scan(&st.Lat, &st.Lon, &st.Operator, &st.Source, &st.Neighborhood, &st.Address, &st.SourceDate); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *PGStore) SitesByCell(ctx context.Context) (map[string]int, error) {
	query := `SELECT h3_cell, count(*) FROM mobile_sites WHERE h3_cell IS NOT NULL GROUP BY h3_cell`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var cell string
		var n int
		if err := rows.Scan(&cell, &n); err != nil {
			return nil, err
		}
		out[cell] = n
	}
	return out, rows.Err()
}

func (s *PGStore) Resources(ctx context.Context, f CellFilter, kind string) ([]Resource, error) {
	query := `SELECT id, kind, COALESCE(name,''), COALESCE(address,''), COALESCE(phone,''),
			COALESCE(latitude,0), COALESCE(longitude,0), details, status, reported_at
		FROM resources
		WHERE ($1::text IS NULL OR kind = $1)
		ORDER BY reported_at DESC
		LIMIT 500`
	var kindFilter any
	if kind != "" {
		kindFilter = kind
	}
	rows, err := s.pool.Query(ctx, query, kindFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Resource
	for rows.Next() {
		var r Resource
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.Address, &r.Phone,
			&r.Lat, &r.Lon, &r.Details, &r.Status, &r.ReportedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) InsertResource(ctx context.Context, r Resource) (int64, error) {
	status := r.Status
	if status == "" {
		status = "pending"
	}
	query := `INSERT INTO resources (kind, name, address, phone, latitude, longitude, geom, details, status)
		VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($6,$5),4326), $7, $8)
		RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, query, r.Kind, nilStr(r.Name), nilStr(r.Address), nilStr(r.Phone),
		r.Lat, r.Lon, r.Details, status).Scan(&id)
	return id, err
}

func (s *PGStore) UpdateResourceStatus(ctx context.Context, id int64, status string) error {
	query := `UPDATE resources SET status = $1 WHERE id = $2`
	_, err := s.pool.Exec(ctx, query, status, id)
	return err
}

func (s *PGStore) UpdateResourceDetails(ctx context.Context, id int64, details map[string]any) error {
	var current map[string]any
	err := s.pool.QueryRow(ctx, `SELECT details FROM resources WHERE id = $1`, id).Scan(&current)
	if err != nil {
		current = make(map[string]any)
	}
	if current == nil {
		current = make(map[string]any)
	}
	for k, v := range details {
		current[k] = v
	}
	query := `UPDATE resources SET details = $1 WHERE id = $2`
	_, err = s.pool.Exec(ctx, query, current, id)
	return err
}

func (s *PGStore) InsertResourceValidation(ctx context.Context, resourceID int64, voteType, ip, userAgent, fingerprint string) (bool, error) {
	_, err := s.pool.Exec(ctx, `INSERT INTO resource_validations (resource_id, vote_type, ip, user_agent, fingerprint) VALUES ($1, $2, $3, $4, $5)`,
		resourceID, voteType, ip, userAgent, fingerprint)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}



// Coverage devuelve el snapshot oficial de cobertura municipal, con
// filtros opcionales de municipio (substring), operador y tecnología.
func (s *PGStore) Coverage(ctx context.Context, municipality, operator, technology string) ([]CoverageRow, error) {
	query := `SELECT dane_code, COALESCE(municipality,''), COALESCE(technology,''),
			COALESCE(signal_level,0), COALESCE(area_km2,0),
			COALESCE(area_pct_claro,0), COALESCE(area_pct_movistar,0),
			COALESCE(area_pct_tigo,0), COALESCE(area_pct_wom,0)
		FROM official_coverage
		WHERE ($1 = '' OR municipality ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR area_pct_claro > 0 OR area_pct_movistar > 0 OR area_pct_tigo > 0 OR area_pct_wom > 0)
		  AND ($3 = '' OR technology = $3)
		ORDER BY municipality, technology, dane_code
		LIMIT 5000`
	rows, err := s.pool.Query(ctx, query, municipality, operator, technology)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoverageRow
	for rows.Next() {
		var c CoverageRow
		if err := rows.Scan(&c.DaneCode, &c.Municipality, &c.Technology, &c.SignalLevel,
			&c.AreaKM2, &c.PctClaro, &c.PctMovistar, &c.PctTigo, &c.PctWom); err != nil {
			return nil, err
		}
		if operator != "" {
			switch operator {
			case observe.OpClaro:
				if c.PctClaro <= 0 {
					continue
				}
			case observe.OpMovistar:
				if c.PctMovistar <= 0 {
					continue
				}
			case observe.OpTigo:
				if c.PctTigo <= 0 {
					continue
				}
			case observe.OpWom:
				if c.PctWom <= 0 {
					continue
				}
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OfficialSites devuelve el número de sitios reportado por operador y
// municipio (último trimestre importado). El CSV fuente desglosa por
// tecnología×propio/coubicación; se agrega a una fila por operador.
func (s *PGStore) OfficialSites(ctx context.Context, municipality string) ([]OfficialSitesRow, error) {
	query := `SELECT dane_code, municipality, operator,
			SUM(sitios)::int,
			bool_or(tech_2g), bool_or(tech_3g), bool_or(tech_4g), bool_or(tech_5g)
		FROM official_sites
		WHERE ($1 = '' OR municipality ILIKE '%' || $1 || '%')
		GROUP BY dane_code, municipality, operator
		ORDER BY municipality, operator, SUM(sitios) DESC
		LIMIT 5000`
	rows, err := s.pool.Query(ctx, query, municipality)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfficialSitesRow
	for rows.Next() {
		var r OfficialSitesRow
		if err := rows.Scan(&r.DaneCode, &r.Municipality, &r.Operator, &r.Sites,
			&r.Tech2G, &r.Tech3G, &r.Tech4G, &r.Tech5G); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryMappings devuelve los mappings ASN->operador desde la tabla.
func (s *PGStore) QueryMappings(ctx context.Context) ([]struct {
	ASN        int
	Operator   string
	Mobile     bool
	Confidence float64
	Source     string
}, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT asn, operator, mobile, confidence, COALESCE(source,'') FROM asn_operator_mapping`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []struct {
		ASN        int
		Operator   string
		Mobile     bool
		Confidence float64
		Source     string
	}{}
	for rows.Next() {
		var r struct {
			ASN        int
			Operator   string
			Mobile     bool
			Confidence float64
			Source     string
		}
		if err := rows.Scan(&r.ASN, &r.Operator, &r.Mobile, &r.Confidence, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nilStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nowMinus(d time.Duration) time.Time {
	return time.Now().UTC().Add(-d)
}

var _ = fmt.Sprintf
