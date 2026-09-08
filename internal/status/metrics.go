package status

import (
	"math"
	"strings"
	"sync"

	"dax-kiro-proxy/internal/kirofeature"
)

type TurnRecord struct {
	Sequence     uint64               `json:"sequence"`
	Scope        string               `json:"scope"`
	Model        string               `json:"model"`
	Multiplier   *float64             `json:"multiplier,omitempty"`
	SessionState string               `json:"session_state"`
	ElapsedMS    int64                `json:"elapsed_ms"`
	Effort       kirofeature.Status   `json:"effort"`
	Metadata     kirofeature.Metadata `json:"metadata"`
}
type MetricsPage struct {
	Records []TurnRecord `json:"records"`
	Dropped uint64       `json:"dropped"`
}
type TurnQueue struct {
	mu                sync.Mutex
	records           []TurnRecord
	latest            *TurnRecord
	sequence, dropped uint64
}

func NewTurnQueue() *TurnQueue { return &TurnQueue{records: make([]TurnRecord, 0, 32)} }
func (q *TurnQueue) Push(record TurnRecord) bool {
	if !validRecord(record) {
		return false
	}
	record = copyRecord(record)
	record.Effort.Model = record.Model
	record.Effort.Reason = ""
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.sequence >= 9007199254740991 {
		return false
	}
	q.sequence++
	record.Sequence = q.sequence
	if len(q.records) == 32 {
		copy(q.records, q.records[1:])
		q.records = q.records[:31]
		q.dropped++
	}
	q.records = append(q.records, record)
	q.latest = &record
	return true
}
func (q *TurnQueue) Drain() MetricsPage {
	q.mu.Lock()
	defer q.mu.Unlock()
	page := MetricsPage{Records: make([]TurnRecord, len(q.records)), Dropped: q.dropped}
	for i, r := range q.records {
		page.Records[i] = copyRecord(r)
	}
	clear(q.records)
	q.records = q.records[:0]
	return page
}
func (q *TurnQueue) Latest() *TurnRecord {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.latest == nil {
		return nil
	}
	copy := copyRecord(*q.latest)
	return &copy
}
func copyRecord(r TurnRecord) TurnRecord {
	r.Metadata = r.Metadata.Copy()
	if r.Multiplier != nil {
		n := *r.Multiplier
		r.Multiplier = &n
	}
	return r
}
func validRecord(r TurnRecord) bool {
	if len(r.Scope) != 64 || len(r.Model) > 256 || !strings.HasPrefix(r.Model, "claude-dax-") || r.ElapsedMS < 0 || r.ElapsedMS > 7200000 || !r.Metadata.Valid() {
		return false
	}
	for _, c := range []byte(r.Scope) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	for _, c := range []byte(r.Model) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	switch r.SessionState {
	case "created", "reused", "loaded":
	default:
		return false
	}
	switch r.Effort.State {
	case kirofeature.Unknown, kirofeature.Current, kirofeature.Unsupported, kirofeature.Unavailable, kirofeature.Configured:
	default:
		return false
	}
	if kirofeature.Normalize(r.Effort.Requested) != r.Effort.Requested || kirofeature.Normalize(r.Effort.Applied) != r.Effort.Applied {
		return false
	}
	if r.Multiplier != nil && (math.IsNaN(*r.Multiplier) || math.IsInf(*r.Multiplier, 0) || *r.Multiplier < 0 || *r.Multiplier > 1e12) {
		return false
	}
	return true
}
