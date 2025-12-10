package leader

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"
)

func (e *kvElection) watchLoop(ctx context.Context) {
	watcher, err := e.kv.Watch(e.key)
	if err != nil {
		log := e.getLogger()
		log.Error("watch_failed",
			append(e.logWithContext(ctx),
				zap.Error(err),
				zap.String("key", e.key),
			)...,
		)
		return
	}
	defer watcher.Stop()

	log := e.getLogger()
	log.Debug("watch_started",
		append(e.logWithContext(ctx),
			zap.String("key", e.key),
		)...,
	)

	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-watcher.Updates():
			if !ok {
				log := e.getLogger()
				log.Debug("watch_closed",
					append(e.logWithContext(ctx))...,
				)
				return
			}
			e.handleWatchEvent(entry)
		}
	}
}

// handleWatchEvent processes watch events and triggers re-election when appropriate.
func (e *kvElection) handleWatchEvent(entry Entry) {
	if entry == nil {
		log := e.getLogger()
		log.Debug("watch_event_key_deleted",
			append(e.logWithContext(e.ctx),
				zap.String("key", e.key),
			)...,
		)
		go e.attemptAcquireWithRetry(e.ctx)
		return
	}

	valueBytes := entry.Value()
	if len(valueBytes) == 0 {
		log := e.getLogger()
		log.Debug("watch_event_key_empty",
			append(e.logWithContext(e.ctx),
				zap.String("key", e.key),
			)...,
		)
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
		log := e.getLogger()
		log.Info("leader_changed",
			append(e.logWithContext(e.ctx),
				zap.String("old_leader_id", currentLeaderID),
				zap.String("new_leader_id", newLeaderID),
				zap.Uint64("revision", entry.Revision()),
			)...,
		)
		e.leaderID.Store(newLeaderID)
		e.revision.Store(entry.Revision())
		return
	}

	e.revision.Store(entry.Revision())
}
