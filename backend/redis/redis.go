package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/theworker02/shiftlock"
)

/*
Durability notes:
Redis is an excellent coordination backend for low-latency leases, but by default
it prioritizes speed over synchronous durability. For ShiftLock ownership:

  - Enable AOF with appendfsync everysec (or always) if ownership must survive
    Redis process restart.
  - Prefer Redis Cluster / Sentinel for availability; fencing tokens still protect
    against split-brain writers at the application layer.
  - Lease TTL + fencing tokens together: TTL recovers from crashed owners; fencing
    tokens reject stale owners after network partitions.
  - This backend uses Lua for atomic CAS of claim records. Do not bypass Lua with
    multi-key pipelines from application code.
*/

// Client is the minimal Redis surface used by this backend.
type Client interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
	Close() error
}

// Backend stores claims in Redis using Lua CAS scripts.
type Backend struct {
	client Client
	prefix string
	gens   map[string]*shiftlock.Generation // generations kept locally + optional Redis
}

// Option configures the redis backend.
type Option func(*Backend)

// WithPrefix sets key prefix (default "shiftlock").
func WithPrefix(p string) Option {
	return func(b *Backend) { b.prefix = p }
}

// New creates a Redis backend. client may be a go-redis adapter (see Adapter).
func New(client Client, opts ...Option) *Backend {
	b := &Backend{client: client, prefix: "shiftlock", gens: make(map[string]*shiftlock.Generation)}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Backend) claimKey(name string) string {
	return fmt.Sprintf("%s:claim:%s", b.prefix, name)
}

func (b *Backend) genKey(id string) string {
	return fmt.Sprintf("%s:gen:%s", b.prefix, id)
}

type claimJSON struct {
	Name             string `json:"name"`
	OwnerGeneration  string `json:"owner_generation"`
	FencingToken     uint64 `json:"fencing_token"`
	Phase            string `json:"phase"`
	AcquiredAtUnix   int64  `json:"acquired_at"`
	ExpiresAtUnix    int64  `json:"expires_at"`
	PreviousOwner    string `json:"previous_owner"`
	PendingSuccessor string `json:"pending_successor"`
	DrainStatus      string `json:"drain_status"`
	TransferStatus   string `json:"transfer_status"`
	LastHeartbeatUnix int64 `json:"last_heartbeat"`
	Reason           string `json:"reason"`
	Version          uint64 `json:"version"`
}

func fromJSON(s string) (*shiftlock.ClaimRecord, error) {
	var j claimJSON
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		return nil, err
	}
	r := &shiftlock.ClaimRecord{
		Name: j.Name, OwnerGeneration: j.OwnerGeneration, FencingToken: shiftlock.FencingToken(j.FencingToken),
		Phase: shiftlock.ClaimPhase(j.Phase), PreviousOwner: j.PreviousOwner, PendingSuccessor: j.PendingSuccessor,
		DrainStatus: j.DrainStatus, TransferStatus: j.TransferStatus, Reason: shiftlock.TransitionReason(j.Reason),
		Version: j.Version,
	}
	if j.AcquiredAtUnix > 0 {
		r.AcquiredAt = time.Unix(0, j.AcquiredAtUnix)
	}
	if j.ExpiresAtUnix > 0 {
		r.ExpiresAt = time.Unix(0, j.ExpiresAtUnix)
	}
	if j.LastHeartbeatUnix > 0 {
		r.LastHeartbeat = time.Unix(0, j.LastHeartbeatUnix)
	}
	return r, nil
}

const acquireScript = `
local key = KEYS[1]
local gen = ARGV[1]
local ttl_ns = tonumber(ARGV[2])
local now_ns = tonumber(ARGV[3])
local raw = redis.call('GET', key)
local obj
if not raw then
  obj = {name=ARGV[4], owner_generation='', fencing_token=0, phase='unowned', previous_owner='', pending_successor='', drain_status='', transfer_status='', reason='', version=0, acquired_at=0, expires_at=0, last_heartbeat=0}
else
  obj = cjson.decode(raw)
end
if obj.expires_at > 0 and now_ns > obj.expires_at and (obj.phase == 'owned' or obj.phase == 'reserved' or obj.phase == 'draining') then
  obj.previous_owner = obj.owner_generation
  obj.owner_generation = ''
  obj.pending_successor = ''
  obj.phase = 'unowned'
  obj.reason = 'expired'
  obj.version = obj.version + 1
end
if obj.phase == 'owned' or obj.phase == 'reserved' or obj.phase == 'draining' then
  if obj.owner_generation == gen then
    obj.expires_at = now_ns + ttl_ns
    obj.last_heartbeat = now_ns
    obj.reason = 'renewed'
    obj.version = obj.version + 1
    redis.call('SET', key, cjson.encode(obj))
    return cjson.encode(obj)
  end
  return cjson.encode({__err='held', data=obj})
end
obj.previous_owner = obj.owner_generation
obj.owner_generation = gen
obj.fencing_token = obj.fencing_token + 1
obj.phase = 'owned'
obj.acquired_at = now_ns
obj.expires_at = now_ns + ttl_ns
obj.last_heartbeat = now_ns
obj.pending_successor = ''
obj.transfer_status = ''
obj.drain_status = ''
obj.reason = 'acquired'
obj.version = obj.version + 1
obj.name = ARGV[4]
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

const releaseScript = `
local key = KEYS[1]
local gen = ARGV[1]
local token = tonumber(ARGV[2])
local raw = redis.call('GET', key)
if not raw then return cjson.encode({__err='not_found'}) end
local obj = cjson.decode(raw)
if obj.fencing_token ~= token then return cjson.encode({__err='stale'}) end
if obj.owner_generation ~= gen then return cjson.encode({__err='not_owner'}) end
obj.previous_owner = obj.owner_generation
obj.owner_generation = ''
obj.pending_successor = ''
obj.phase = 'unowned'
obj.transfer_status = ''
obj.drain_status = ''
obj.reason = 'released'
obj.version = obj.version + 1
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

const prepareScript = `
local key = KEYS[1]
local from_gen = ARGV[1]
local to_gen = ARGV[2]
local token = tonumber(ARGV[3])
local ttl_ns = tonumber(ARGV[4])
local now_ns = tonumber(ARGV[5])
local raw = redis.call('GET', key)
if not raw then return cjson.encode({__err='not_found'}) end
local obj = cjson.decode(raw)
if obj.expires_at > 0 and now_ns > obj.expires_at and (obj.phase == 'owned' or obj.phase == 'reserved' or obj.phase == 'draining') then
  obj.previous_owner = obj.owner_generation
  obj.owner_generation = ''
  obj.pending_successor = ''
  obj.phase = 'unowned'
  obj.reason = 'expired'
  obj.version = obj.version + 1
  redis.call('SET', key, cjson.encode(obj))
  return cjson.encode({__err='not_owner'})
end
if obj.owner_generation ~= from_gen then return cjson.encode({__err='not_owner'}) end
if obj.fencing_token ~= token then return cjson.encode({__err='stale'}) end
if obj.phase == 'reserved' and obj.pending_successor ~= '' and obj.pending_successor ~= to_gen then
  return cjson.encode({__err='concurrent'})
end
obj.phase = 'reserved'
obj.pending_successor = to_gen
obj.transfer_status = 'prepared'
obj.drain_status = 'complete'
obj.expires_at = now_ns + ttl_ns
obj.last_heartbeat = now_ns
obj.reason = 'transfer_prepared'
obj.version = obj.version + 1
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

const commitScript = `
local key = KEYS[1]
local from_gen = ARGV[1]
local to_gen = ARGV[2]
local token = tonumber(ARGV[3])
local ttl_ns = tonumber(ARGV[4])
local now_ns = tonumber(ARGV[5])
local raw = redis.call('GET', key)
if not raw then return cjson.encode({__err='not_found'}) end
local obj = cjson.decode(raw)
if obj.expires_at > 0 and now_ns > obj.expires_at and (obj.phase == 'owned' or obj.phase == 'reserved' or obj.phase == 'draining') then
  obj.previous_owner = obj.owner_generation
  obj.owner_generation = ''
  obj.pending_successor = ''
  obj.phase = 'unowned'
  obj.reason = 'expired'
  obj.version = obj.version + 1
  redis.call('SET', key, cjson.encode(obj))
  return cjson.encode({__err='no_transfer'})
end
if obj.phase ~= 'reserved' then return cjson.encode({__err='no_transfer'}) end
if obj.owner_generation ~= from_gen or obj.pending_successor ~= to_gen then
  return cjson.encode({__err='concurrent'})
end
if obj.fencing_token ~= token then return cjson.encode({__err='stale'}) end
obj.previous_owner = obj.owner_generation
obj.owner_generation = to_gen
obj.fencing_token = obj.fencing_token + 1
obj.phase = 'owned'
obj.pending_successor = ''
obj.transfer_status = 'committed'
obj.drain_status = ''
obj.acquired_at = now_ns
obj.expires_at = now_ns + ttl_ns
obj.last_heartbeat = now_ns
obj.reason = 'transfer_committed'
obj.version = obj.version + 1
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

const abortScript = `
local key = KEYS[1]
local from_gen = ARGV[1]
local token = tonumber(ARGV[2])
local raw = redis.call('GET', key)
if not raw then return cjson.encode({__err='not_found'}) end
local obj = cjson.decode(raw)
if obj.phase ~= 'reserved' then
  if obj.owner_generation == from_gen and obj.phase == 'owned' then return cjson.encode(obj) end
  return cjson.encode({__err='no_transfer'})
end
if obj.fencing_token ~= token then return cjson.encode({__err='stale'}) end
if obj.owner_generation ~= from_gen then return cjson.encode({__err='not_owner'}) end
obj.pending_successor = ''
obj.phase = 'owned'
obj.transfer_status = 'aborted'
obj.reason = 'transfer_aborted'
obj.version = obj.version + 1
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

const renewScript = `
local key = KEYS[1]
local gen = ARGV[1]
local token = tonumber(ARGV[2])
local ttl_ns = tonumber(ARGV[3])
local now_ns = tonumber(ARGV[4])
local raw = redis.call('GET', key)
if not raw then return cjson.encode({__err='not_found'}) end
local obj = cjson.decode(raw)
if obj.expires_at > 0 and now_ns > obj.expires_at then
  obj.previous_owner = obj.owner_generation
  obj.owner_generation = ''
  obj.phase = 'unowned'
  obj.reason = 'expired'
  obj.version = obj.version + 1
  redis.call('SET', key, cjson.encode(obj))
  return cjson.encode({__err='not_owner'})
end
if obj.owner_generation ~= gen then return cjson.encode({__err='not_owner'}) end
if obj.fencing_token ~= token then return cjson.encode({__err='stale'}) end
obj.expires_at = now_ns + ttl_ns
obj.last_heartbeat = now_ns
obj.reason = 'renewed'
obj.version = obj.version + 1
redis.call('SET', key, cjson.encode(obj))
return cjson.encode(obj)
`

func parseResult(v any) (*shiftlock.ClaimRecord, error) {
	s, ok := v.(string)
	if !ok {
		if b, ok := v.([]byte); ok {
			s = string(b)
		} else {
			return nil, &shiftlock.Error{Op: "redis", Err: shiftlock.ErrBackend, Message: fmt.Sprintf("unexpected type %T", v)}
		}
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return nil, err
	}
	if e, ok := probe["__err"].(string); ok {
		switch e {
		case "held":
			// data nested?
			return nil, shiftlock.ErrClaimHeld
		case "stale":
			return nil, shiftlock.ErrStaleToken
		case "not_owner":
			return nil, shiftlock.ErrNotOwner
		case "not_found":
			return nil, shiftlock.ErrClaimNotFound
		case "concurrent":
			return nil, shiftlock.ErrConcurrentTransfer
		case "no_transfer":
			return nil, shiftlock.ErrNoTransfer
		default:
			return nil, &shiftlock.Error{Op: "redis", Err: shiftlock.ErrBackend, Message: e}
		}
	}
	return fromJSON(s)
}

func (b *Backend) RegisterGeneration(ctx context.Context, gen shiftlock.Generation) error {
	b.gens[gen.ID] = &gen
	data, _ := json.Marshal(gen)
	return b.client.Set(ctx, b.genKey(gen.ID), string(data), 0)
}

func (b *Backend) UpdateGeneration(ctx context.Context, gen shiftlock.Generation) error {
	return b.RegisterGeneration(ctx, gen)
}

func (b *Backend) GetGeneration(ctx context.Context, generationID string) (*shiftlock.Generation, error) {
	s, err := b.client.Get(ctx, b.genKey(generationID))
	if err != nil {
		return nil, shiftlock.ErrGenerationNotFound
	}
	var g shiftlock.Generation
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (b *Backend) GetClaim(ctx context.Context, claimName string) (*shiftlock.ClaimRecord, error) {
	s, err := b.client.Get(ctx, b.claimKey(claimName))
	if err != nil || s == "" {
		return nil, shiftlock.ErrClaimNotFound
	}
	return fromJSON(s)
}

func (b *Backend) AcquireClaim(ctx context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return rec, err
	}
	now := time.Now().UnixNano()
	v, err := b.client.Eval(ctx, acquireScript, []string{b.claimKey(req.ClaimName)},
		req.GenerationID, req.TTL.Nanoseconds(), now, req.ClaimName)
	if err != nil {
		return nil, mapRedis("AcquireClaim", err)
	}
	// held returns special encoding — check
	if s, ok := asString(v); ok {
		var probe map[string]any
		_ = json.Unmarshal([]byte(s), &probe)
		if probe["__err"] == "held" {
			if data, ok := probe["data"].(map[string]any); ok {
				raw, _ := json.Marshal(data)
				rec, _ := fromJSON(string(raw))
				b.storeOp(ctx, req.OperationID, rec, shiftlock.ErrClaimHeld)
				return rec, shiftlock.ErrClaimHeld
			}
			b.storeOp(ctx, req.OperationID, nil, shiftlock.ErrClaimHeld)
			return nil, shiftlock.ErrClaimHeld
		}
	}
	out, err := parseResult(v)
	if err != nil {
		b.storeOp(ctx, req.OperationID, out, err)
		return out, err
	}
	b.storeOp(ctx, req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) RenewClaim(ctx context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return rec, err
	}
	now := time.Now().UnixNano()
	v, err := b.client.Eval(ctx, renewScript, []string{b.claimKey(req.ClaimName)},
		req.GenerationID, uint64(req.Token), req.TTL.Nanoseconds(), now)
	if err != nil {
		return nil, mapRedis("RenewClaim", err)
	}
	out, err := parseResult(v)
	if err == nil {
		b.storeOp(ctx, req.OperationID, out, nil)
	}
	return out, err
}

func (b *Backend) PrepareTransfer(ctx context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return rec, err
	}
	now := time.Now().UnixNano()
	v, err := b.client.Eval(ctx, prepareScript, []string{b.claimKey(req.ClaimName)},
		req.FromGeneration, req.ToGeneration, uint64(req.Token), req.TTL.Nanoseconds(), now)
	if err != nil {
		return nil, mapRedis("PrepareTransfer", err)
	}
	out, err := parseResult(v)
	if err == nil {
		b.storeOp(ctx, req.OperationID, out, nil)
	}
	return out, err
}

func (b *Backend) CommitTransfer(ctx context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return rec, err
	}
	// State-based idempotency when the success response was lost but OpID was not stored.
	if cur, err := b.GetClaim(ctx, req.ClaimName); err == nil {
		if cur.Phase == shiftlock.ClaimOwned && cur.OwnerGeneration == req.ToGeneration &&
			cur.FencingToken == req.ExpectedToken+1 && cur.TransferStatus == "committed" {
			b.storeOp(ctx, req.OperationID, cur, nil)
			return cur, nil
		}
	}
	now := time.Now().UnixNano()
	v, err := b.client.Eval(ctx, commitScript, []string{b.claimKey(req.ClaimName)},
		req.FromGeneration, req.ToGeneration, uint64(req.ExpectedToken), req.TTL.Nanoseconds(), now)
	if err != nil {
		return nil, mapRedis("CommitTransfer", err)
	}
	out, err := parseResult(v)
	if err == nil {
		b.storeOp(ctx, req.OperationID, out, nil)
	} else {
		b.storeOp(ctx, req.OperationID, out, err)
	}
	return out, err
}

func (b *Backend) AbortTransfer(ctx context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return rec, err
	}
	v, err := b.client.Eval(ctx, abortScript, []string{b.claimKey(req.ClaimName)},
		req.FromGeneration, uint64(req.ExpectedToken))
	if err != nil {
		return nil, mapRedis("AbortTransfer", err)
	}
	out, err := parseResult(v)
	if err == nil {
		b.storeOp(ctx, req.OperationID, out, nil)
	}
	return out, err
}

func (b *Backend) ReleaseClaim(ctx context.Context, req shiftlock.ReleaseRequest) error {
	if _, err, ok := b.recallOp(ctx, req.OperationID); ok {
		return err
	}
	v, err := b.client.Eval(ctx, releaseScript, []string{b.claimKey(req.ClaimName)},
		req.GenerationID, uint64(req.Token))
	if err != nil {
		return mapRedis("ReleaseClaim", err)
	}
	_, err = parseResult(v)
	b.storeOp(ctx, req.OperationID, nil, err)
	return err
}

func (b *Backend) WatchClaim(ctx context.Context, claimName string) (<-chan shiftlock.ClaimEvent, error) {
	ch := make(chan shiftlock.ClaimEvent, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var last uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rec, err := b.GetClaim(ctx, claimName)
				if err != nil {
					continue
				}
				if rec.Version != last {
					last = rec.Version
					select {
					case ch <- shiftlock.ClaimEvent{Claim: *rec, Time: time.Now(), Reason: rec.Reason}:
					default:
					}
				}
			}
		}
	}()
	return ch, nil
}

func (b *Backend) Close() error {
	return b.client.Close()
}

func asString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []byte:
		return string(t), true
	default:
		return "", false
	}
}

func mapRedis(op string, err error) error {
	return &shiftlock.Error{Op: "redis." + op, Err: shiftlock.ErrBackend, Message: err.Error()}
}

// MemoryClient is an in-process Redis substitute for unit tests.
type MemoryClient struct {
	data map[string]string
}

// NewMemoryClient returns a test double implementing Client.
func NewMemoryClient() *MemoryClient {
	return &MemoryClient{data: make(map[string]string)}
}

func (m *MemoryClient) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("nil")
	}
	return v, nil
}

func (m *MemoryClient) Set(_ context.Context, key string, value any, _ time.Duration) error {
	m.data[key] = fmt.Sprint(value)
	return nil
}

func (m *MemoryClient) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func (m *MemoryClient) Close() error { return nil }

// Eval for MemoryClient implements only the claim scripts via Go fallbacks —
// for unit tests prefer the memory backend. This evaluates by delegating to
// a simplified Go path for Get/Set based scripts used in contract tests when
// SHIFTLOCK_REDIS_URL is unset — actually we skip Lua. Return error to force
// skip in integration; for contract, use shiftlock/backend/memory.
func (m *MemoryClient) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return nil, errors.New("memory client does not execute Lua; use integration Redis or memory backend")
}

var _ shiftlock.Backend = (*Backend)(nil)
var _ Client = (*MemoryClient)(nil)
