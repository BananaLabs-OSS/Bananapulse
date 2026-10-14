package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/vmihailenco/msgpack/v5"
	"math"
	"regexp"
)

const Version = "telemetry.v1"

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Owner struct{ db *sql.DB }

func openOwner(path string) (*Owner, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	o := &Owner{db}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS telemetry_modules(id TEXT PRIMARY KEY, config TEXT NOT NULL, state TEXT NOT NULL, attempted INTEGER NOT NULL DEFAULT 0, ok INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS telemetry_samples(module TEXT NOT NULL, at INTEGER NOT NULL, metrics TEXT NOT NULL, PRIMARY KEY(module,at));
 CREATE TABLE IF NOT EXISTS telemetry_pending(id TEXT PRIMARY KEY, module TEXT NOT NULL, command BLOB NOT NULL, expires INTEGER NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return o, nil
}
func (o *Owner) configure(c Config) error {
	if c.Version != Version || !validID.MatchString(c.ID) || !validID.MatchString(c.ComponentID) {
		return errors.New("invalid telemetry configuration")
	}
	if c.TTL == 0 {
		c.TTL = 120
	}
	if c.Retention == 0 {
		c.Retention = 7
	}
	if c.TTL < 1 || c.TTL > 86400 || c.Retention < 1 || c.Retention > 365 {
		return errors.New("invalid telemetry retention or ttl")
	}
	p := c.Policy
	if p.Enabled && (!validID.MatchString(p.MetricID) || !validID.MatchString(p.SourceID) || p.BadFor < 0 || p.GoodFor < 0 || p.BadFor > 86400 || p.GoodFor > 86400 || (p.Direction != "" && p.Direction != "above") || math.IsNaN(p.Below) || math.IsNaN(p.Above) || math.IsNaN(p.RecoverAbove) || math.IsNaN(p.RecoverBelow) || math.IsInf(p.Below, 0) || math.IsInf(p.Above, 0) || math.IsInf(p.RecoverAbove, 0) || math.IsInf(p.RecoverBelow, 0)) {
		return errors.New("invalid telemetry policy")
	}
	if p.Enabled && ((p.Direction == "above" && p.RecoverBelow >= p.Above) || (p.Direction != "above" && p.RecoverAbove <= p.Below)) {
		return errors.New("policy requires hysteresis")
	}
	raw, _ := json.Marshal(c)
	var previous string
	err := o.db.QueryRow("SELECT config FROM telemetry_modules WHERE id=?", c.ID).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && previous != string(raw) {
		return errors.New("telemetry configuration is immutable; use a new module identity")
	}
	_, err = o.db.Exec("INSERT OR IGNORE INTO telemetry_modules(id,config,state) VALUES(?,?,?)", c.ID, string(raw), "{}")
	return err
}
func loadModule(tx *sql.Tx, id string) (Config, State, error) {
	var config, state string
	var c Config
	var s State
	err := tx.QueryRow("SELECT config,state FROM telemetry_modules WHERE id=?", id).Scan(&config, &state)
	if err != nil {
		return c, s, err
	}
	if err = json.Unmarshal([]byte(config), &c); err != nil {
		return c, s, err
	}
	err = json.Unmarshal([]byte(state), &s)
	return c, s, err
}
func advance(s State, metrics []Metric, now int64, p Policy, ttl int64) (State, string, error) {
	if now-s.LastSample > ttl {
		s.BadSince = 0
		s.GoodSince = 0
	}
	s.LastSample = now
	values := map[string]float64{}
	for _, m := range metrics {
		values[m.ID] = m.Value
	}
	if p.ReadyMetric != "" && values[p.ReadyMetric] != 1 {
		s.BadSince = 0
		s.GoodSince = 0
		return s, "", nil
	}
	value, ok := values[p.MetricID]
	if !ok {
		return s, "", errors.New("policy metric missing")
	}
	bad, good := value < p.Below, value >= p.RecoverAbove
	if p.Direction == "above" {
		bad, good = value > p.Above, value < p.RecoverBelow
	}
	switch {
	case bad:
		s.GoodSince = 0
		if s.BadSince == 0 {
			s.BadSince = now
		}
		if now-s.BadSince >= p.BadFor {
			s.Degraded = true
		}
	case good:
		s.BadSince = 0
		if s.GoodSince == 0 {
			s.GoodSince = now
		}
		if now-s.GoodSince >= p.GoodFor {
			s.Degraded = false
		}
	default:
		s.BadSince = 0
		s.GoodSince = 0
	}
	if s.Degraded {
		return s, "degraded", nil
	}
	return s, "ok", nil
}
func validateMetrics(metrics []Metric) error {
	if len(metrics) == 0 || len(metrics) > 32 {
		return errors.New("invalid metric count")
	}
	seen := map[string]bool{}
	for _, m := range metrics {
		if !validID.MatchString(m.ID) || seen[m.ID] || len(m.Label) > 128 || len(m.Unit) > 32 || math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			return errors.New("invalid metric")
		}
		seen[m.ID] = true
	}
	return nil
}
func (o *Owner) ingest(sample Sample) error {
	if sample.Version != Version || sample.At <= 0 {
		return errors.New("invalid telemetry sample")
	}
	if sample.OK {
		if err := validateMetrics(sample.Metrics); err != nil {
			return err
		}
	} else if len(sample.Metrics) > 0 {
		return errors.New("failed sample must not contain metrics")
	}
	tx, err := o.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, state, err := loadModule(tx, sample.ModuleID)
	if err != nil {
		return err
	}
	if sample.ComponentID != c.ComponentID {
		return errors.New("component mismatch")
	}
	var previous int64
	if err = tx.QueryRow("SELECT attempted FROM telemetry_modules WHERE id=?", c.ID).Scan(&previous); err != nil {
		return err
	}
	if sample.At <= previous {
		return nil
	} // Replays and late polls cannot rewrite current health.
	if sample.OK {
		metrics, _ := json.Marshal(sample.Metrics)
		if _, err = tx.Exec("INSERT INTO telemetry_samples VALUES(?,?,?)", c.ID, sample.At, string(metrics)); err != nil {
			return err
		}
		if c.Policy.Enabled && sample.Publish {
			var signal string
			state, signal, err = advance(state, sample.Metrics, sample.At, c.Policy, c.TTL)
			if err != nil {
				return err
			}
			if signal != "" && !c.EvaluationOnly {
				idRaw := sha256.Sum256([]byte(c.ID + "/" + string(metrics) + "/" + jsonInt(sample.At)))
				ident := hex.EncodeToString(idRaw[:])
				command := map[string]any{"version": "monitor.v1", "id": ident, "kind": "ingest_observation", "at_unix": sample.At, "ingest": map[string]any{"observation_id": ident, "source_id": c.Policy.SourceID, "component_id": c.ComponentID, "signal": signal, "detail": "Telemetry performance rule", "observed_at_unix": sample.At, "expires_at_unix": sample.At + c.TTL}}
				raw, e := msgpack.Marshal(command)
				if e != nil {
					return e
				}
				if _, err = tx.Exec("INSERT OR IGNORE INTO telemetry_pending VALUES(?,?,?,?)", ident, c.ID, raw, sample.At+c.TTL); err != nil {
					return err
				}
			}
		}
	}
	if !sample.OK || !sample.Publish {
		state.BadSince = 0
		state.GoodSince = 0
		if _, err = tx.Exec("DELETE FROM telemetry_pending WHERE module=?", c.ID); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(state)
	if _, err = tx.Exec("UPDATE telemetry_modules SET state=?,attempted=?,ok=? WHERE id=?", string(raw), sample.At, sample.OK, c.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM telemetry_samples WHERE module=? AND at<?", c.ID, sample.At-c.Retention*86400); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM telemetry_pending WHERE module=? AND expires<=?", c.ID, sample.At); err != nil {
		return err
	}
	return tx.Commit()
}
func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }
func (o *Owner) snapshot(q Query) (Snapshot, error) {
	result := Snapshot{Version: Version, ModuleID: q.ModuleID, Stale: true, Metrics: []Metric{}}
	if q.Version != Version || q.Now <= 0 {
		return result, errors.New("invalid telemetry query")
	}
	var raw, policyState string
	err := o.db.QueryRow("SELECT config,state,attempted,ok FROM telemetry_modules WHERE id=?", q.ModuleID).Scan(&raw, &policyState, &result.LastAttempt, &result.LastPollOK)
	if err != nil {
		return result, err
	}
	var c Config
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return result, err
	}
	result.ComponentID = c.ComponentID
	var state State
	if err = json.Unmarshal([]byte(policyState), &state); err != nil {
		return result, err
	}
	result.PolicySignal = "ok"
	if state.Degraded {
		result.PolicySignal = "degraded"
	}
	err = o.db.QueryRow("SELECT at,metrics FROM telemetry_samples WHERE module=? ORDER BY at DESC LIMIT 1", q.ModuleID).Scan(&result.ObservedAt, &raw)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.ExpiresAt = result.ObservedAt + c.TTL
	result.Stale = result.ExpiresAt <= q.Now
	err = json.Unmarshal([]byte(raw), &result.Metrics)
	return result, err
}
func (o *Owner) history(q Query) ([]HistorySample, error) {
	if q.BucketSeconds != 0 {
		return o.bucketHistory(q)
	}
	if q.Version != Version || q.Now <= 0 {
		return nil, errors.New("invalid telemetry query")
	}
	if q.Since < q.Now-86400 {
		q.Since = q.Now - 86400
	}
	result := []HistorySample{}
	cursor := q.Now + 1
	bytes := 0
	// Page by timestamp so the SQLite host never has to allocate an entire long
	// history. Bound JSON size as well as row count for the private bridge.
	for len(result) < 2000 {
		rows, err := o.db.Query("SELECT at,metrics FROM telemetry_samples WHERE module=? AND at>=? AND at<? ORDER BY at DESC LIMIT 16", q.ModuleID, q.Since, cursor)
		if err != nil {
			return nil, err
		}
		batch := []HistorySample{}
		for rows.Next() {
			var item HistorySample
			var raw string
			if err = rows.Scan(&item.At, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err = json.Unmarshal([]byte(raw), &item.Metrics); err != nil {
				rows.Close()
				return nil, err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		full := false
		for _, item := range batch {
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			if bytes+len(encoded)+2 > 512<<10 || len(result) == 2000 {
				full = true
				break
			}
			result = append(result, item)
			bytes += len(encoded) + 1
			cursor = item.At
		}
		if full || len(batch) < 16 {
			break
		}
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func (o *Owner) pending(q Query) ([]Pending, error) {
	if q.Version != Version || q.Now <= 0 {
		return nil, errors.New("invalid telemetry query")
	}
	rows, err := o.db.Query("SELECT id,command FROM telemetry_pending WHERE module=? AND expires>? ORDER BY rowid LIMIT 256", q.ModuleID, q.Now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pending{}
	for rows.Next() {
		var p Pending
		var raw []byte
		if err = rows.Scan(&p.ID, &raw); err != nil {
			return nil, err
		}
		p.Command = string(raw)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (o *Owner) ack(a Ack) error {
	if a.Version != Version || !validID.MatchString(a.ModuleID) || len(a.ID) != 64 {
		return errors.New("invalid delivery acknowledgement")
	}
	_, err := o.db.Exec("DELETE FROM telemetry_pending WHERE module=? AND id=?", a.ModuleID, a.ID)
	return err
}

// Import is a fenced cutover: history can be seeded only before the first live poll.
type Import struct {
	Version   string          `msgpack:"version"`
	ModuleID  string          `msgpack:"moduleId"`
	Samples   []HistorySample `msgpack:"samples"`
	Finish    bool            `msgpack:"finish"`
	State     State           `msgpack:"state"`
	Attempted int64           `msgpack:"attempted"`
	OK        bool            `msgpack:"ok"`
}

func (o *Owner) importHistory(in Import) error {
	if in.Version != Version || len(in.Samples) > 500 || in.Attempted < 0 {
		return errors.New("invalid history import")
	}
	tx, err := o.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var attempted int64
	if err = tx.QueryRow("SELECT attempted FROM telemetry_modules WHERE id=?", in.ModuleID).Scan(&attempted); err != nil {
		return err
	}
	if attempted != 0 {
		return errors.New("history import closed after cutover")
	}
	for _, s := range in.Samples {
		if s.At <= 0 {
			return errors.New("invalid imported timestamp")
		}
		if err = validateMetrics(s.Metrics); err != nil {
			return err
		}
		raw, _ := json.Marshal(s.Metrics)
		if _, err = tx.Exec("INSERT OR IGNORE INTO telemetry_samples VALUES(?,?,?)", in.ModuleID, s.At, string(raw)); err != nil {
			return err
		}
	}
	if in.Finish {
		if in.Attempted <= 0 {
			return errors.New("cutover requires timestamp")
		}
		state, _ := json.Marshal(in.State)
		if _, err = tx.Exec("UPDATE telemetry_modules SET state=?,attempted=?,ok=? WHERE id=?", string(state), in.Attempted, in.OK, in.ModuleID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// bucketHistory provides bounded week-long summaries without moving raw history to the frontend.
func (o *Owner) bucketHistory(q Query) ([]HistorySample, error) {
	if q.Version != Version || q.Now <= 0 || q.BucketSeconds < 60 || q.BucketSeconds > 3600 {
		return nil, errors.New("invalid history bucket")
	}
	if q.Since < q.Now-7*86400 {
		q.Since = q.Now - 7*86400
	}
	// A week contains at most 672 quarter-hour buckets. Each query materializes only 16 buckets.
	if (q.Now-q.Since)/q.BucketSeconds > 720 {
		return nil, errors.New("too many history buckets")
	}
	result := []HistorySample{}
	cursor := q.Now + 1
	bytes := 0
	for len(result) < 720 {
		rows, err := o.db.Query(`SELECT bucket, json_group_array(json_object('id',id,'label',label,'unit',unit,'value',mean,'min',low,'max',high,'samples',n)) FROM (
   SELECT CAST(at / ? AS INTEGER) AS bucket, json_extract(j.value,'$.id') AS id,
    json_extract(j.value,'$.label') AS label, json_extract(j.value,'$.unit') AS unit,
    AVG(json_extract(j.value,'$.value')) AS mean, MIN(json_extract(j.value,'$.value')) AS low, MAX(json_extract(j.value,'$.value')) AS high, COUNT(*) AS n
   FROM telemetry_samples, json_each(metrics) AS j WHERE module=? AND at>=? AND at<?
   GROUP BY bucket,id,label,unit ORDER BY bucket DESC LIMIT 512
  ) GROUP BY bucket ORDER BY bucket DESC LIMIT 16`, q.BucketSeconds, q.ModuleID, q.Since, cursor)
		if err != nil {
			return nil, err
		}
		batch := []HistorySample{}
		for rows.Next() {
			var bucket int64
			var raw string
			if err = rows.Scan(&bucket, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			item := HistorySample{At: bucket * q.BucketSeconds, BucketSeconds: q.BucketSeconds}
			if err = json.Unmarshal([]byte(raw), &item.Metrics); err != nil {
				rows.Close()
				return nil, err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		full := false
		for _, item := range batch {
			encoded, _ := json.Marshal(item)
			if bytes+len(encoded)+2 > 512<<10 {
				full = true
				break
			}
			result = append(result, item)
			bytes += len(encoded) + 1
			cursor = item.At
		}
		if full || len(batch) < 16 {
			break
		}
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}
