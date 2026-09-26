package bifrost

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	vkCacheTTL = 30 * time.Second
)

var (
	vkCacheLock sync.Mutex
	vkCache     = map[string]cachedVirtualKey{}
)

type cachedVirtualKey struct {
	value   string
	fetched time.Time
}

// VirtualKeyValue returns the secret value of a Bifrost virtual key, which Obot sends on
// behalf of callers that a budget binding assigns to that key. The value never leaves
// the process.
func (e *Engine) VirtualKeyValue(ctx context.Context, id string) (string, error) {
	if !e.Ready() {
		return "", errors.New("LLM gateway is not ready")
	}

	vkCacheLock.Lock()
	if c, ok := vkCache[id]; ok && time.Since(c.fetched) < vkCacheTTL {
		vkCacheLock.Unlock()
		return c.value, nil
	}
	vkCacheLock.Unlock()

	store := e.server.Config.ConfigStore
	if store == nil {
		return "", errors.New("LLM gateway has no config store")
	}
	vk, err := store.GetVirtualKey(ctx, id)
	if err != nil {
		return "", err
	}
	if vk.IsActive != nil && !*vk.IsActive {
		return "", errors.New("virtual key is inactive")
	}
	value := vk.Value.GetValue()

	vkCacheLock.Lock()
	vkCache[id] = cachedVirtualKey{value: value, fetched: time.Now()}
	vkCacheLock.Unlock()
	return value, nil
}

// ForgetVirtualKey drops a cached virtual key value, e.g. after it is rotated or deleted.
func ForgetVirtualKey(id string) {
	vkCacheLock.Lock()
	defer vkCacheLock.Unlock()
	if id == "" {
		clear(vkCache)
		return
	}
	delete(vkCache, id)
}
