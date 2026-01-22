package core

import (
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

// startSmartSenderProcessing starts the SmartSender chat processing goroutine
func (c *Core) startSmartSenderProcessing() {
	if c.smartSender == nil || c.zohoFunctions == nil {
		c.log.Debug("SmartSender integration not configured, skipping")
		return
	}

	pollInterval := c.ssPollInterval
	if pollInterval == 0 {
		pollInterval = 60 * time.Second
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

	chats, err := c.smartSender.GetAllChats()
	if err != nil {
		log.With(sl.Err(err)).Error("failed to fetch chats")
		return
	}

	msgProcessedCount := 0
	for _, chat := range chats {
		count, err := c.processChat(chat)
		if err != nil {
			log.With(
				sl.Err(err),
				slog.String("chat_id", string(chat.ID)),
			).Error("failed to process chat")
		}
		msgProcessedCount += count
	}

	if msgProcessedCount > 0 {
		log.Debug("processed messages", slog.Int("count", msgProcessedCount))
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
	if err := c.zohoFunctions.SendMessages(chat.Contact.OriginalID, zohoMessages); err != nil {
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
