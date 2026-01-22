package core

import (
	"errors"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/services"
)

// startSmartSenderProcessing starts the SmartSender chat processing goroutine
func (c *Core) startSmartSenderProcessing() {
	if c.smartSender == nil || c.zohoFunctions == nil {
		c.log.Debug("SmartSender integration not configured, skipping")
		return
	}

	pollInterval := c.ssPollInterval
	if pollInterval == 0 {
		pollInterval = 120 * time.Second
	}

	// Load state from MongoDB on startup
	c.loadSSStateFromMongo()

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		c.log.Info("SmartSender processing started", slog.Duration("interval", pollInterval))

		// Run once at startup
		c.processSmartSenderChats()

		for {
			select {
			case <-c.stopCh:
				c.log.Info("SmartSender processing stopped")
				return
			case <-ticker.C:
				c.processSmartSenderChats()
			}
		}
	}()
}

// loadSSStateFromMongo loads all last processed times from MongoDB into cache
func (c *Core) loadSSStateFromMongo() {
	if c.mongoRepo == nil {
		return
	}

	states, err := c.mongoRepo.GetAllSSLastProcessedTimes()
	if err != nil {
		c.log.With(sl.Err(err)).Warn("failed to load SmartSender state from MongoDB")
		return
	}

	c.ssLastProcessedMu.Lock()
	for chatID, t := range states {
		c.ssLastProcessed[chatID] = t
	}
	c.ssLastProcessedMu.Unlock()

	c.log.Debug("loaded SmartSender state from MongoDB", slog.Int("chats", len(states)))
}

// processSmartSenderChats fetches all chats and processes new messages
func (c *Core) processSmartSenderChats() {
	log := c.log.With(sl.Module("smartsender"))

	// Check global rate-limit pause
	c.ssRateLimitMu.RLock()
	if !c.ssRateLimitUntil.IsZero() && time.Now().Before(c.ssRateLimitUntil) {
		wait := time.Until(c.ssRateLimitUntil)
		c.ssRateLimitMu.RUnlock()
		log.Info("SmartSender processing paused due to previous rate limit", slog.Duration("wait", wait))
		return
	}
	c.ssRateLimitMu.RUnlock()

	chats, err := c.smartSender.GetAllChats()
	if err != nil {
		// Check if it's a services.APIError and honor RetryAfter for rate limits
		var apiErr *services.APIError
		if errors.As(err, &apiErr) {
			if apiErr.Status == 423 || apiErr.Status == 429 {
				retryAfter := apiErr.RetryAfter
				if retryAfter == 0 {
					if apiErr.Status == 423 {
						retryAfter = 720 * time.Second
					} else {
						retryAfter = 5 * time.Second
					}
				}
				log.With(sl.Err(err)).Warn("SmartSender rate limit received; pausing processing", slog.Duration("retry_after", retryAfter))
				// Set global pause until time
				c.ssRateLimitMu.Lock()
				c.ssRateLimitUntil = time.Now().Add(retryAfter)
				c.ssRateLimitMu.Unlock()
				return
			}
		}

		log.With(sl.Err(err)).Error("failed to fetch chats")
		return
	}

	// If we have a resume-from chat ID saved from previous interrupted run, start from that chat
	c.ssResumeMu.RLock()
	resumeID := c.ssResumeFromChatID
	c.ssResumeMu.RUnlock()
	startIndex := 0
	if resumeID != "" {
		for i, ch := range chats {
			if string(ch.ID) == resumeID {
				startIndex = i
				break
			}
		}
		// clear resume marker - we are about to resume
		c.ssResumeMu.Lock()
		c.ssResumeFromChatID = ""
		c.ssResumeMu.Unlock()
	}

	// Process chats starting from startIndex
	chats = chats[startIndex:]

	msgProcessedCount := 0
	// safety limits to avoid hammering SmartSender
	const (
		maxChatsPerCycle  = 100
		sleepBetweenChats = 500 * time.Millisecond
	)
	processedChats := 0
	for _, chat := range chats {
		// stop if we've been asked to stop
		select {
		case <-c.stopCh:
			break
		default:
		}

		// check global rate-limit pause before each chat
		c.ssRateLimitMu.RLock()
		if !c.ssRateLimitUntil.IsZero() && time.Now().Before(c.ssRateLimitUntil) {
			wait := time.Until(c.ssRateLimitUntil)
			c.ssRateLimitMu.RUnlock()
			log.Info("SmartSender processing paused due to previous rate limit", slog.Duration("wait", wait))
			break
		}
		c.ssRateLimitMu.RUnlock()

		if processedChats >= maxChatsPerCycle {
			//log.Info("reached max chats per cycle, will resume next tick",
			//	slog.Int("processed", processedChats),
			//	slog.Int("remain", len(chats)-processedChats))
			// Save resume position to continue from this chat next tick
			c.ssResumeMu.Lock()
			c.ssResumeFromChatID = string(chat.ID)
			c.ssResumeMu.Unlock()
			break
		}

		count, err := c.processChat(chat)
		processedChats++

		if err != nil {
			// if this is a rate-limit API error, set global pause, save resume position and stop processing
			var apiErr *services.APIError
			if errors.As(err, &apiErr) {
				if apiErr.Status == 423 || apiErr.Status == 429 {
					retryAfter := apiErr.RetryAfter
					if retryAfter == 0 {
						if apiErr.Status == 423 {
							retryAfter = 720 * time.Second
						} else {
							retryAfter = 5 * time.Second
						}
					}
					log.With(sl.Err(err)).Warn("SmartSender rate limit received while processing chat; pausing processing", slog.Duration("retry_after", retryAfter), slog.String("chat_id", string(chat.ID)))
					// set global pause until time
					c.ssRateLimitMu.Lock()
					c.ssRateLimitUntil = time.Now().Add(retryAfter)
					c.ssRateLimitMu.Unlock()
					// save resume position (start from this chat next time)
					c.ssResumeMu.Lock()
					c.ssResumeFromChatID = string(chat.ID)
					c.ssResumeMu.Unlock()
					break
				}
			}

			log.With(
				sl.Err(err),
				slog.String("chat_id", string(chat.ID)),
			).Error("failed to process chat")
		}

		msgProcessedCount += count

		// small pause between chat processing to avoid bursts
		time.Sleep(sleepBetweenChats)
	}

	if msgProcessedCount > 0 {
		log.Debug("processed messages", slog.Int("count", msgProcessedCount))
	}

	// If we processed whole provided list without interruption, clear any resume marker
	if processedChats > 0 && processedChats < len(chats) {
		// we stopped early (either reached maxChatsPerCycle or a pause) - resume marker may be set already
	} else {
		// full pass completed or no chats; ensure resume marker cleared
		c.ssResumeMu.Lock()
		c.ssResumeFromChatID = ""
		c.ssResumeMu.Unlock()
	}
}

// processChat processes a single chat - fetches and sends new messages to Zoho
func (c *Core) processChat(chat entity.SSChat) (int, error) {
	// Get the last processed timestamp for this chat
	c.ssLastProcessedMu.RLock()
	lastProcessedTime := c.ssLastProcessed[string(chat.ID)]
	c.ssLastProcessedMu.RUnlock()

	// Fetch messages created after the last processed time
	messages, err := c.smartSender.GetMessagesAfterTime(string(chat.ID), lastProcessedTime)
	if err != nil {
		return 0, err
	}

	if len(messages) == 0 {
		return 0, nil
	}

	// Extract text messages and track the latest timestamp
	var zohoMessages []entity.ZohoMessageItem
	var latestTime time.Time

	for _, msg := range messages {
		if msg.Content.Type != "text" {
			continue
		}

		content := msg.Content.Resource.Parameters.Content
		if content == "" {
			continue
		}

		zohoMessages = append(zohoMessages, entity.ZohoMessageItem{
			MessageID: string(msg.ID),
			ChatID:    string(chat.ID),
			Content:   content,
			Sender:    msg.Sender.FullName,
		})

		if msg.CreatedAt.After(latestTime) {
			latestTime = msg.CreatedAt
		}
	}

	if len(zohoMessages) == 0 {
		return 0, nil
	}

	// Send messages to Zoho
	if err := c.zohoFunctions.SendMessages(string(chat.Contact.OriginalID), zohoMessages); err != nil {
		return 0, err
	}

	// Update the last processed timestamp in cache and MongoDB
	if !latestTime.IsZero() {
		c.ssLastProcessedMu.Lock()
		c.ssLastProcessed[string(chat.ID)] = latestTime
		c.ssLastProcessedMu.Unlock()

		// Save to MongoDB
		if c.mongoRepo != nil {
			if err := c.mongoRepo.SetSSLastProcessedTime(string(chat.ID), latestTime); err != nil {
				c.log.With(sl.Err(err)).Warn("failed to save SmartSender state to MongoDB")
			}
		}
	}

	return len(zohoMessages), nil
}
