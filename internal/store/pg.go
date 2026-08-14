package store

import (
	"context"
	"database/sql"
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
		save_data, call_signal, operator_user, probe_1k_ms, probe_4k_ms, transfer_estimate_bps, client_ip
	) VALUES ($1,$2,$3,$4,$5, ST_SetSRID(ST_MakePoint($4,$3),4326), $6,
		$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, query,
		o.ReceivedAt, o.ObservedAt, o.Latitude, o.Longitude, o.Accuracy, o.H3Cell,
		o.ASN, o.Operator, o.Mobile, o.HttpRTTMin, o.HttpRTTMedian, o.Jitter, o.SuccessRatio,
		o.Samples, o.FailedRequests, nilStr(o.EffectiveType), o.BrowserRTT, o.BrowserDownlink,
		o.SaveData, nilStr(o.CallSignal), nilStr(o.OperatorUser), o.Probe1kMs, o.Probe4kMs, o.TransferEstimateBps, nilStr(o.ClientIP),
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
			save_data, call_signal, operator_user, probe_1k_ms, probe_4k_ms, transfer_estimate_bps, client_ip
		) VALUES ($1,$2,$3,$4,$5, ST_SetSRID(ST_MakePoint($4,$3),4326), $6,
			$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`
		batch.Queue(query,
			o.ReceivedAt, o.ObservedAt, o.Latitude, o.Longitude, o.Accuracy, o.H3Cell,
			o.ASN, o.Operator, o.Mobile, o.HttpRTTMin, o.HttpRTTMedian, o.Jitter, o.SuccessRatio,
			o.Samples, o.FailedRequests, nilStr(o.EffectiveType), o.BrowserRTT, o.BrowserDownlink,
			o.SaveData, nilStr(o.CallSignal), nilStr(o.OperatorUser), o.Probe1kMs, o.Probe4kMs, o.TransferEstimateBps, nilStr(o.ClientIP))
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

func (s *PGStore) ObservationHistory(ctx context.Context, f ObservationHistoryFilter) (ObservationHistoryPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var where []string
	args := make([]any, 0, 8)
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if f.Operator != "" {
		add("LOWER(COALESCE(operator, '')) = LOWER($%d)", f.Operator)
	}
	if f.From != nil {
		add("observed_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("observed_at <= $%d", *f.To)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like, like, like, like, like)
		base := len(args) - 5
		where = append(where, fmt.Sprintf(
			"(LOWER(COALESCE(operator, '')) LIKE $%d OR LOWER(COALESCE(operator_user, '')) LIKE $%d OR LOWER(COALESCE(h3_cell, '')) LIKE $%d OR LOWER(COALESCE(client_ip, '')) LIKE $%d OR LOWER(COALESCE(call_signal, '')) LIKE $%d OR LOWER(COALESCE(effective_type, '')) LIKE $%d)",
			base, base+1, base+2, base+3, base+4, base+5,
		))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`SELECT
		id, received_at, observed_at, latitude, longitude, COALESCE(accuracy, 0),
		h3_cell, asn, COALESCE(operator, ''), COALESCE(mobile, false),
		COALESCE(http_rtt_min, 0), COALESCE(http_rtt_median, 0), COALESCE(jitter, 0),
		COALESCE(success_ratio, 0), COALESCE(samples, 0), COALESCE(failed_requests, 0),
		COALESCE(effective_type, ''), COALESCE(browser_rtt, 0), COALESCE(browser_downlink, 0),
		COALESCE(save_data, false), COALESCE(call_signal, ''), COALESCE(operator_user, ''),
		COALESCE(probe_1k_ms, 0), COALESCE(probe_4k_ms, 0), COALESCE(transfer_estimate_bps, 0),
		COALESCE(client_ip, '')::text,
		count(*) OVER() AS total
		FROM observations
		%s
		ORDER BY observed_at DESC, id DESC
		LIMIT $%d OFFSET $%d`, whereClause, len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return ObservationHistoryPage{}, err
	}
	defer rows.Close()

	out := ObservationHistoryPage{
		Items:  []ObservationHistoryRow{},
		Limit:  limit,
		Offset: offset,
	}

	for rows.Next() {
		var raw struct {
			ID                  int64
			ReceivedAt          time.Time
			ObservedAt          time.Time
			Latitude            float64
			Longitude           float64
			Accuracy            float64
			H3Cell              string
			ASN                 sql.NullInt64
			Operator            string
			Mobile              bool
			HttpRTTMin          float64
			HttpRTTMedian       float64
			Jitter              float64
			SuccessRatio        float64
			Samples             int
			FailedRequests      int
			EffectiveType       string
			BrowserRTT          float64
			BrowserDownlink     float64
			SaveData            bool
			CallSignal          string
			OperatorUser        string
			Probe1kMs           float64
			Probe4kMs           float64
			TransferEstimateBps float64
			ClientIP            string
			Total               int
		}
		if err := rows.Scan(
			&raw.ID, &raw.ReceivedAt, &raw.ObservedAt, &raw.Latitude, &raw.Longitude, &raw.Accuracy,
			&raw.H3Cell, &raw.ASN, &raw.Operator, &raw.Mobile, &raw.HttpRTTMin, &raw.HttpRTTMedian,
			&raw.Jitter, &raw.SuccessRatio, &raw.Samples, &raw.FailedRequests, &raw.EffectiveType,
			&raw.BrowserRTT, &raw.BrowserDownlink, &raw.SaveData, &raw.CallSignal, &raw.OperatorUser,
			&raw.Probe1kMs, &raw.Probe4kMs, &raw.TransferEstimateBps, &raw.ClientIP, &raw.Total,
		); err != nil {
			return ObservationHistoryPage{}, err
		}
		if out.Total == 0 {
			out.Total = raw.Total
		}
		row := ObservationHistoryRow{
			ID:                  raw.ID,
			ReceivedAt:          raw.ReceivedAt,
			ObservedAt:          raw.ObservedAt,
			Latitude:            raw.Latitude,
			Longitude:           raw.Longitude,
			Accuracy:            raw.Accuracy,
			H3Cell:              raw.H3Cell,
			Operator:            raw.Operator,
			Mobile:              raw.Mobile,
			HttpRTTMin:          raw.HttpRTTMin,
			HttpRTTMedian:       raw.HttpRTTMedian,
			Jitter:              raw.Jitter,
			SuccessRatio:        raw.SuccessRatio,
			Samples:             raw.Samples,
			FailedRequests:      raw.FailedRequests,
			EffectiveType:       raw.EffectiveType,
			BrowserRTT:          raw.BrowserRTT,
			BrowserDownlink:     raw.BrowserDownlink,
			SaveData:            raw.SaveData,
			CallSignal:          raw.CallSignal,
			OperatorUser:        raw.OperatorUser,
			Probe1kMs:           raw.Probe1kMs,
			Probe4kMs:           raw.Probe4kMs,
			TransferEstimateBps: raw.TransferEstimateBps,
			ClientIP:            raw.ClientIP,
		}
		if raw.ASN.Valid {
			asn := int(raw.ASN.Int64)
			row.ASN = &asn
		}
		out.Items = append(out.Items, row)
	}
	return out, rows.Err()
}

func (s *PGStore) AdminOverview(ctx context.Context) (AdminOverview, error) {
	var out AdminOverview

	var latest sql.NullTime
	if err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE observed_at >= now() - interval '24 hours')::int,
			COUNT(*) FILTER (WHERE observed_at >= now() - interval '7 days')::int,
			MAX(observed_at)
		FROM observations`,
	).Scan(&out.ObservationsTotal, &out.Observations24h, &out.Observations7d, &latest); err != nil {
		return out, err
	}
	if latest.Valid {
		t := latest.Time
		out.LatestObservationAt = &t
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE LOWER(COALESCE(status, '')) = 'pending')::int,
			COUNT(*) FILTER (WHERE LOWER(COALESCE(status, '')) = 'approved')::int,
			COUNT(*) FILTER (WHERE LOWER(COALESCE(status, '')) = 'rejected')::int
		FROM resources`,
	).Scan(&out.ResourcesTotal, &out.ResourcesPending, &out.ResourcesApproved, &out.ResourcesRejected); err != nil {
		return out, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT operator)::int
		FROM observations
		WHERE operator IS NOT NULL AND operator <> ''`,
	).Scan(&out.ActiveOperatorsCount); err != nil {
		return out, err
	}

	return out, nil
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

func (s *PGStore) InsertSismoEvents(ctx context.Context, events []SismoEvent) ([]SismoEvent, error) {
	var inserted []SismoEvent
	for _, e := range events {
		tag, err := s.pool.Exec(ctx, `INSERT INTO sismo_events
			(id, mag, mag_type, depth, lat, lon, place, local_time, utc_time, event_type, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (id) DO NOTHING`,
			e.ID, e.Mag, nilStr(e.MagType), e.Depth, e.Lat, e.Lon, nilStr(e.Place),
			nilStr(e.LocalTime), nilStr(e.UTCTime), nilStr(e.EventType), nilStr(e.Status))
		if err != nil {
			return inserted, err
		}
		if tag.RowsAffected() > 0 {
			inserted = append(inserted, e)
		}
	}
	return inserted, nil
}

func (s *PGStore) RecentSismos(ctx context.Context, limit int) ([]SismoEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, COALESCE(mag,0), COALESCE(mag_type,''), COALESCE(depth,0),
			COALESCE(lat,0), COALESCE(lon,0), COALESCE(place,''), COALESCE(local_time,''),
			COALESCE(utc_time,''), COALESCE(event_type,''), COALESCE(status,''), detected_at
		FROM sismo_events ORDER BY detected_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SismoEvent
	for rows.Next() {
		var e SismoEvent
		if err := rows.Scan(&e.ID, &e.Mag, &e.MagType, &e.Depth, &e.Lat, &e.Lon,
			&e.Place, &e.LocalTime, &e.UTCTime, &e.EventType, &e.Status, &e.DetectedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) UpsertPushSubscription(ctx context.Context, sub PushSubscription) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, device)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (endpoint) DO UPDATE SET
			p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
			device = COALESCE(EXCLUDED.device, push_subscriptions.device),
			last_error = NULL, last_error_at = NULL`,
		sub.Endpoint, sub.P256DH, sub.Auth, nilStr(sub.Device))
	return err
}

func (s *PGStore) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, `SELECT endpoint, p256dh, auth, COALESCE(device,''), created_at
		FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.Device, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *PGStore) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

func (s *PGStore) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (s *PGStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// CoverageSynthesis devuelve la cobertura derivada de los mapas públicos de
// operadores para un municipio (dane_code), ordenada por operador/tecnología.
func (s *PGStore) CoverageSynthesis(ctx context.Context, daneCode string) ([]CoverageSynthesisRow, error) {
	query := `SELECT dane_code, COALESCE(department,''), municipality, operator, technology,
			COALESCE(covered_ratio,0), COALESCE(covered_km2,0), COALESCE(area_km2,0)
		FROM coverage_synthesis
		WHERE dane_code = $1
		ORDER BY operator, technology`
	rows, err := s.pool.Query(ctx, query, daneCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoverageSynthesisRow
	for rows.Next() {
		var r CoverageSynthesisRow
		if err := rows.Scan(&r.DaneCode, &r.Department, &r.Municipality, &r.Operator,
			&r.Technology, &r.CoveredRatio, &r.CoveredKM2, &r.AreaKM2); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CoveragePoint devuelve los operadores/tecnologías que declaran cobertura
// en una celda H3 res 7.
func (s *PGStore) CoveragePoint(ctx context.Context, h3Cell string) ([]CoverageCellRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT operator, technology FROM coverage_synthesis_cells
		 WHERE h3 = $1 ORDER BY operator, technology`, h3Cell)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoverageCellRow
	for rows.Next() {
		var r CoverageCellRow
		if err := rows.Scan(&r.Operator, &r.Technology); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CoverageMeta describe la última carga de síntesis de cobertura.
func (s *PGStore) CoverageMeta(ctx context.Context) (*CoverageMeta, error) {
	var m CoverageMeta
	err := s.pool.QueryRow(ctx,
		`SELECT generated_at, source, h3_res FROM coverage_synthesis_meta WHERE id = 1`).
		Scan(&m.GeneratedAt, &m.Source, &m.H3Res)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
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
