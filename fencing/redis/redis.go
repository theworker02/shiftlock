package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// ErrStaleFencingToken is returned when a mutation carries a stale token.
var ErrStaleFencingToken = errors.New("fencing/redis: stale fencing token")

// Client is the minimal Redis surface for fenced writes.
type Client interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
}

// Resource stores a fenced value in Redis.
type Resource struct {
	Client Client
	Key    string
}

const writeScript = `
local key = KEYS[1]
local tok = tonumber(ARGV[1])
local val = ARGV[2]
local raw = redis.call('GET', key)
local cur = 0
if raw then
  local sep = string.find(raw, '|', 1, true)
  if sep then cur = tonumber(string.sub(raw, 1, sep-1)) or 0 end
end
if tok < cur then return 0 end
redis.call('SET', key, tostring(tok) .. '|' .. val)
return 1
`

// Write mutates only if token is not stale.
func (r *Resource) Write(ctx context.Context, token shiftlock.FencingToken, value string) error {
	if token.Zero() {
		return ErrStaleFencingToken
	}
	v, err := r.Client.Eval(ctx, writeScript, []string{r.Key}, uint64(token), value)
	if err != nil {
		return err
	}
	switch n := v.(type) {
	case int64:
		if n == 0 {
			return ErrStaleFencingToken
		}
	case int:
		if n == 0 {
			return ErrStaleFencingToken
		}
	default:
		return fmt.Errorf("fencing/redis: unexpected eval result %T", v)
	}
	return nil
}
