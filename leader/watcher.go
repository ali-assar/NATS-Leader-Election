package leader

import (
	"context"
	"encoding/json"
)

func (e *kvElection) watchLoop(ctx context.Context) {
	watcher, err := e.kv.Watch(e.key)
	if err != nil {
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-watcher.Updates():
			if !ok {
				return
			}
			e.handleWatchEvent(entry)
		}
	}
}

// handleWatchEvent processes watch events and triggers re-election when appropriate.
func (e *kvElection) handleWatchEvent(entry Entry) {
	if entry == nil {
		go e.attemptAcquireWithRetry(e.ctx)
		return
	}

	valueBytes := entry.Value()
	if len(valueBytes) == 0 {
		go e.attemptAcquireWithRetry(e.ctx)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(valueBytes, &payload); err != nil {
		return
	}

	leaderIDInterface, ok := payload["id"]
	if !ok {
		return
	}

	newLeaderID, ok := leaderIDInterface.(string)
	if !ok {
		return
	}

	if e.IsLeader() {
		return
	}

	currentLeaderID := e.LeaderID()
	if currentLeaderID != newLeaderID {
		e.leaderID.Store(newLeaderID)
		e.revision.Store(entry.Revision())
		return
	}

	e.revision.Store(entry.Revision())
}
