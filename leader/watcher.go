package leader

import (
	"context"
	"encoding/json"
	"time"

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

	checkTicker := time.NewTicker(500 * time.Millisecond)
	defer checkTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-watcher.Updates():
			if !ok {
				log := e.getLogger()
				log.Debug("watch_closed",
					e.logWithContext(ctx)...,
				)
				// When watcher closes, check if key still exists
				// If not, trigger re-election
				if !e.IsLeader() {
					go e.checkKeyAndReelect(ctx)
				}
				return
			}
			e.handleWatchEvent(entry)
		case <-checkTicker.C:
			// Periodic check: if we're a follower and key doesn't exist, trigger re-election
			// This handles cases where NATS watchers don't send deletion events
			if !e.IsLeader() {
				e.checkKeyAndReelect(ctx)
			}
		}
	}
}

// checkKeyAndReelect checks if the key exists and triggers re-election if it doesn't.
// This is a fallback for cases where NATS watchers don't reliably send deletion events.
func (e *kvElection) checkKeyAndReelect(ctx context.Context) {
	if e.IsLeader() {
		return
	}

	entry, err := e.kv.Get(e.key)
	if err != nil {
		// Key doesn't exist - trigger re-election
		log := e.getLogger()
		log.Debug("key_not_found_triggering_reelection",
			append(e.logWithContext(ctx),
				zap.Error(err),
			)...,
		)
		go e.attemptAcquireWithRetry(ctx)
		return
	}

	if entry == nil || len(entry.Value()) == 0 {
		// Key is empty - trigger re-election
		log := e.getLogger()
		log.Debug("key_empty_triggering_reelection",
			e.logWithContext(ctx)...,
		)
		go e.attemptAcquireWithRetry(ctx)
		return
	}

	// Key exists - check if leader changed
	var payload map[string]interface{}
	if err := json.Unmarshal(entry.Value(), &payload); err != nil {
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

	currentLeaderID := e.LeaderID()
	if currentLeaderID != "" && currentLeaderID != newLeaderID {
		log := e.getLogger()
		log.Info("leader_changed_periodic_check",
			append(e.logWithContext(ctx),
				zap.String("old_leader_id", currentLeaderID),
				zap.String("new_leader_id", newLeaderID),
			)...,
		)
		e.leaderID.Store(newLeaderID)
		e.revision.Store(entry.Revision())
	}
}

// handleWatchEvent processes watch events and triggers re-election when the key is deleted
// or becomes empty. It also updates the leader ID when a new leader is detected.
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

	var payload leadershipPayload
	if err := json.Unmarshal(valueBytes, &payload); err != nil {
		return
	}

	newLeaderID := payload.ID

	// If we're the leader, check if we're still the leader
	if e.IsLeader() {
		// If the new leader ID is different, we've been taken over
		if newLeaderID != e.cfg.InstanceID {
			log := e.getLogger()
			log.Warn("leadership_lost_via_watcher",
				append(e.logWithContext(e.ctx),
					zap.String("new_leader_id", newLeaderID),
					zap.Uint64("revision", entry.Revision()),
				)...,
			)
			e.becomeFollower()
		}
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
	e.leaderID.Store(newLeaderID)
	e.revision.Store(entry.Revision())

	// Check if we should attempt priority takeover
	if e.cfg.AllowPriorityTakeover && e.cfg.Priority > payload.Priority {
		log := e.getLogger()
		log.Info("priority_takeover_opportunity",
			append(e.logWithContext(e.ctx),
				zap.String("current_leader", currentLeaderID),
				zap.Int("current_priority", payload.Priority),
				zap.Int("our_priority", e.cfg.Priority),
			)...,
		)

		// Attempt takeover in a goroutine to avoid blocking watcher
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			if err := e.attemptAcquire(); err != nil {
				// Takeover failed - stay as follower
				log.Debug("priority_takeover_failed",
					append(e.logWithContext(e.ctx),
						zap.Error(err),
					)...,
				)
			}
		}()
	}
}
