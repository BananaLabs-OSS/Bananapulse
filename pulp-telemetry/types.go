package main

// All provider contracts are versioned; secrets and raw provider payloads are never persisted.
type Metric struct {
	Samples int64    `json:"samples,omitempty" msgpack:"samples,omitempty"`
	ID      string   `json:"id" msgpack:"id"`
	Label   string   `json:"label" msgpack:"label"`
	Unit    string   `json:"unit" msgpack:"unit"`
	Value   float64  `json:"value" msgpack:"value"`
	Min     *float64 `json:"min,omitempty" msgpack:"min,omitempty"`
	Max     *float64 `json:"max,omitempty" msgpack:"max,omitempty"`
}
type Policy struct {
	Enabled      bool    `json:"enabled" msgpack:"enabled"`
	MetricID     string  `json:"metricId" msgpack:"metricId"`
	ReadyMetric  string  `json:"readyMetric" msgpack:"readyMetric"`
	Direction    string  `json:"direction" msgpack:"direction"`
	Below        float64 `json:"below" msgpack:"below"`
	Above        float64 `json:"above" msgpack:"above"`
	RecoverAbove float64 `json:"recoverAbove" msgpack:"recoverAbove"`
	RecoverBelow float64 `json:"recoverBelow" msgpack:"recoverBelow"`
	BadFor       int64   `json:"badForSeconds" msgpack:"badForSeconds"`
	GoodFor      int64   `json:"recoveryForSeconds" msgpack:"recoveryForSeconds"`
	SourceID     string  `json:"sourceId" msgpack:"sourceId"`
}
type Config struct {
	EvaluationOnly bool   `json:"evaluationOnly" msgpack:"evaluationOnly"`
	Version        string `json:"version" msgpack:"version"`
	ID             string `json:"id" msgpack:"id"`
	ComponentID    string `json:"componentId" msgpack:"componentId"`
	TTL            int64  `json:"ttlSeconds" msgpack:"ttlSeconds"`
	Retention      int64  `json:"retentionDays" msgpack:"retentionDays"`
	Policy         Policy `json:"policy" msgpack:"policy"`
}
type Sample struct {
	Version     string   `json:"version" msgpack:"version"`
	ModuleID    string   `json:"moduleId" msgpack:"moduleId"`
	ComponentID string   `json:"componentId" msgpack:"componentId"`
	At          int64    `json:"observedAt" msgpack:"observedAt"`
	OK          bool     `json:"ok" msgpack:"ok"`
	Publish     bool     `json:"publish" msgpack:"publish"`
	Metrics     []Metric `json:"metrics" msgpack:"metrics"`
}
type Query struct {
	BucketSeconds int64  `json:"bucketSeconds,omitempty" msgpack:"bucketSeconds,omitempty"`
	Version       string `json:"version" msgpack:"version"`
	ModuleID      string `json:"moduleId" msgpack:"moduleId"`
	Now           int64  `json:"now" msgpack:"now"`
	Since         int64  `json:"since" msgpack:"since"`
}
type State struct {
	BadSince   int64 `json:"badSince,omitempty" msgpack:"badSince,omitempty"`
	GoodSince  int64 `json:"goodSince,omitempty" msgpack:"goodSince,omitempty"`
	LastSample int64 `json:"lastSampleAt,omitempty" msgpack:"lastSampleAt,omitempty"`
	Degraded   bool  `json:"degraded,omitempty" msgpack:"degraded,omitempty"`
}
type Snapshot struct {
	PolicySignal string   `json:"policySignal" msgpack:"policySignal"`
	Version      string   `json:"version" msgpack:"version"`
	ModuleID     string   `json:"moduleId" msgpack:"moduleId"`
	ComponentID  string   `json:"componentId" msgpack:"componentId"`
	ObservedAt   int64    `json:"observedAt" msgpack:"observedAt"`
	ExpiresAt    int64    `json:"expiresAt" msgpack:"expiresAt"`
	Stale        bool     `json:"stale" msgpack:"stale"`
	LastPollOK   bool     `json:"lastPollOk" msgpack:"lastPollOk"`
	LastAttempt  int64    `json:"lastAttemptAt" msgpack:"lastAttemptAt"`
	Metrics      []Metric `json:"metrics" msgpack:"metrics"`
}
type HistorySample struct {
	BucketSeconds int64    `json:"bucketSeconds,omitempty" msgpack:"bucketSeconds,omitempty"`
	At            int64    `json:"observedAt" msgpack:"observedAt"`
	Metrics       []Metric `json:"metrics" msgpack:"metrics"`
}
type Pending struct {
	ID      string `msgpack:"id"`
	Command string `msgpack:"command_msgpack"`
}
type Ack struct {
	Version  string `msgpack:"version"`
	ModuleID string `msgpack:"moduleId"`
	ID       string `msgpack:"id"`
}
